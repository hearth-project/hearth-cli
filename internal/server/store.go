package server

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	_ "modernc.org/sqlite"             // pure-Go SQLite driver
)

//go:embed schema.sql
var schemaSQL string

var (
	errNotFound   = errors.New("not found")
	errExists     = errors.New("already exists")
	errGone       = errors.New("session deleted")
	errConflict   = errors.New("conflict")
	errLocked     = errors.New("leased by another device")
	errInvite     = errors.New("invalid, used or expired invite code")
	errBadRequest = errors.New("bad request")
)

//go:embed schema_postgres.sql
var schemaPostgres string

// store wraps the metadata database and the blob store.
type store struct {
	db    *dbx // writes (SQLite: the single writer connection)
	rdb   *dbx // reads (SQLite: a pool of read-only WAL readers)
	d     dialect
	blobs blobStore
}

// openStore connects to the metadata database and the blob store.
//
//	dbURL:   "" or a path or sqlite:///path  ->  SQLite (default <data>/hearth.db)
//	         postgres://user:pass@host/db    ->  PostgreSQL
//	blobURL: "" or a path or file:///path    ->  local directory (default <data>/objects)
//	         s3://bucket/prefix?endpoint=... ->  S3-compatible object storage
func openStore(dataDir, dbURL, blobURL string) (*store, error) {
	if blobURL == "" {
		blobURL = filepath.Join(dataDir, "objects")
	}
	blobs, err := openBlobs(blobURL)
	if err != nil {
		return nil, err
	}
	var st *store
	if strings.HasPrefix(dbURL, "postgres://") || strings.HasPrefix(dbURL, "postgresql://") {
		st, err = openPostgres(dbURL)
	} else {
		if dbURL == "" {
			dbURL = filepath.Join(dataDir, "hearth.db")
		}
		st, err = openSQLite(strings.TrimPrefix(dbURL, "sqlite://"))
	}
	if err != nil {
		return nil, err
	}
	st.blobs = blobs
	return st, nil
}

func openSQLite(file string) (*store, error) {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, err
	}
	// WAL + synchronous=NORMAL: commits do not fsync the WAL. A power cut can
	// lose the last commits but never corrupts the database, and the sync
	// protocol recovers (clients resume from the server's committed size).
	// Blobs are still fsynced before their rows are committed.
	common := "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)" +
		"&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)&_pragma=mmap_size(268435456)"
	path := "file:" + file
	db, err := sql.Open("sqlite", path+common+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	// One writer connection: SQLite allows a single writer anyway, and this
	// avoids SQLITE_BUSY between concurrent write transactions.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Columns added after the first release of a table.
	for _, c := range []struct{ table, column, def string }{
		{"users", "recovery_salt", "BLOB NOT NULL DEFAULT x''"},
		{"users", "recovery_hash", "BLOB NOT NULL DEFAULT x''"},
		{"users", "enc_vault_key_identity", "BLOB NOT NULL DEFAULT x''"},
		{"users", "identity_salt", "BLOB NOT NULL DEFAULT x''"},
		{"users", "identity_hash", "BLOB NOT NULL DEFAULT x''"},
		{"users", "identity_disabled", "INTEGER NOT NULL DEFAULT 0"},
		{"package_versions", "search_text", "TEXT NOT NULL DEFAULT ''"},
		{"teams", "rev", "INTEGER NOT NULL DEFAULT 0"},
		{"users", "status", "TEXT NOT NULL DEFAULT 'active'"},
		{"users", "quota_bytes", "INTEGER NOT NULL DEFAULT -1"},
		{"sessions", "active_at", "INTEGER NOT NULL DEFAULT 0"},
		{"sessions", "updated_by", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "client_version", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "last_ip", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "offline_at", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := ensureColumn(db, c.table, c.column, c.def); err != nil {
			db.Close()
			return nil, err
		}
	}
	// Sessions from before active_at: last change is the best estimate.
	for _, q := range []string{`UPDATE sessions SET active_at=updated_at WHERE active_at=0`,
		`CREATE INDEX IF NOT EXISTS sessions_by_activity ON sessions(user_id, active_at DESC)`} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	rdb, err := sql.Open("sqlite", path+common+"&_pragma=query_only(1)")
	if err != nil {
		db.Close()
		return nil, err
	}
	n := runtime.GOMAXPROCS(0)
	if n > 32 {
		n = 32
	}
	rdb.SetMaxOpenConns(n)
	rdb.SetMaxIdleConns(n)
	return &store{db: &dbx{db, sqliteDialect}, rdb: &dbx{rdb, sqliteDialect}, d: sqliteDialect}, nil
}

// pgSchemaLock serialises schema setup when several instances start at once.
const pgSchemaLock = 0x61677379 // "agsy"

func openPostgres(dsn string) (*store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(64)
	db.SetMaxIdleConns(16)
	db.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pgSchemaLock); err != nil {
		db.Close()
		return nil, err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pgSchemaLock)
	if _, err := conn.ExecContext(ctx, schemaPostgres); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// One pool serves reads and writes; PostgreSQL handles concurrent writers.
	x := &dbx{db, postgresDialect}
	return &store{db: x, rdb: x, d: postgresDialect}, nil
}

func ensureColumn(db *sql.DB, table, column, def string) error {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		if n == column {
			return nil
		}
	}
	rows.Close()
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
	return err
}

func (s *store) close() error {
	if s.rdb != s.db {
		s.rdb.Close()
	}
	return s.db.Close()
}

func (s *store) readObject(name string) ([]byte, error) { return s.blobs.get(name) }

// setting returns a server-wide value, creating it with mk on first use.
// Safe when several instances start at once: the first insert wins.
func (s *store) setting(key string, mk func() []byte) ([]byte, error) {
	var v []byte
	err := s.db.QueryRow(`SELECT value FROM server_kv WHERE key=?`, key).Scan(&v)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err := s.db.Exec(`INSERT INTO server_kv(key, value) VALUES(?,?) ON CONFLICT DO NOTHING`, key, mk()); err != nil {
		return nil, err
	}
	err = s.db.QueryRow(`SELECT value FROM server_kv WHERE key=?`, key).Scan(&v)
	return v, err
}

// flushDeletes removes blobs queued in pending_deletes. Rows are deleted
// first inside the metadata transaction, blobs afterwards, so a crash leaves
// at worst a queued name that the next run removes. Deletes run in parallel
// (object stores have per-request latency).
func (s *store) flushDeletes() error {
	for {
		rows, err := s.db.Query(`SELECT object FROM pending_deletes LIMIT 512`)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			names = append(names, n)
		}
		rows.Close()
		if len(names) == 0 {
			return nil
		}
		errs := make([]error, len(names))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 16)
		for i, n := range names {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				errs[i] = s.blobs.del(n)
			}()
		}
		wg.Wait()
		for i, n := range names {
			if errs[i] != nil {
				return errs[i]
			}
			if _, err := s.db.Exec(`DELETE FROM pending_deletes WHERE object = ?`, n); err != nil {
				return err
			}
		}
		if len(names) < 512 {
			return nil
		}
	}
}

// queueChunkDeletes moves the chunks matching where into pending_deletes and
// deletes their rows, all inside tx.
func queueChunkDeletes(tx *txx, where string, args ...any) error {
	if _, err := tx.Exec(`INSERT INTO pending_deletes(object) SELECT object FROM session_chunks WHERE `+where+` ON CONFLICT DO NOTHING`, args...); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM session_chunks WHERE `+where, args...)
	return err
}
