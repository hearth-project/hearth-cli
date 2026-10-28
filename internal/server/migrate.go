package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// Storage describes the configured backends (passwords redacted).
func (s *Server) Storage() string { return s.st.String() }

func (s *store) String() string {
	kind := "sqlite"
	if s.d == postgresDialect {
		kind = "postgres"
	}
	return fmt.Sprintf("%s database, blobs %s", kind, s.blobs)
}

// Redact hides the password of a database URL for logs.
func Redact(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "xxx")
	}
	return u.String()
}

// Tables in foreign-key order. pending_deletes is not copied: its blobs
// are garbage.
var migrateTables = []struct {
	name   string
	object string // column naming a blob to copy along
}{
	{"server_kv", ""}, {"users", ""}, {"devices", ""}, {"tokens", ""}, {"invites", ""},
	{"sessions", ""}, {"session_chunks", "object"}, {"session_tombstones", ""},
	{"libraries", "object"}, {"teams", ""}, {"team_members", ""}, {"packages", ""},
	{"package_versions", "object"}, {"secrets", ""}, {"usage_daily", ""},
	{"user_usage_daily", ""}, {"team_invites", ""}, {"team_shares", ""},
	{"admins", ""}, {"admin_audit", ""}, {"activity_daily", ""}, {"user_intents", ""},
}

// MigrateStats summarises a copy.
type MigrateStats struct {
	Rows, Blobs, BlobBytes, MissingBlobs int64
}

// Migrate copies everything from src to an empty dst (any combination of
// SQLite / PostgreSQL and local / S3). The source is read in one snapshot
// so it may keep serving; blobs deleted by GC meanwhile are counted as
// missing. `server backup` uses this with a fresh SQLite directory.
func Migrate(src, dst Config, logf func(string, ...any)) (MigrateStats, error) {
	var stats MigrateStats
	from, err := openStore(src.DataDir, src.Database, src.Blobs)
	if err != nil {
		return stats, fmt.Errorf("source: %w", err)
	}
	defer from.close()
	to, err := openStore(dst.DataDir, dst.Database, dst.Blobs)
	if err != nil {
		return stats, fmt.Errorf("destination: %w", err)
	}
	defer to.close()
	var n int64
	if err := to.db.QueryRow(`SELECT (SELECT COUNT(*) FROM users) + (SELECT COUNT(*) FROM teams)`).Scan(&n); err != nil {
		return stats, err
	}
	if n > 0 {
		return stats, errors.New("destination is not empty")
	}
	// The destination may have created its own prelogin secret on open.
	if _, err := to.db.Exec(`DELETE FROM server_kv`); err != nil {
		return stats, err
	}

	opts := &sql.TxOptions{ReadOnly: true}
	if from.d == postgresDialect {
		opts.Isolation = sql.LevelRepeatableRead
	}
	stx, err := from.rdb.DB.BeginTx(context.Background(), opts)
	if err != nil {
		return stats, err
	}
	defer stx.Rollback()
	snap := &txx{Tx: stx, d: from.d}

	for _, t := range migrateTables {
		rows, err := snap.Query(`SELECT * FROM ` + t.name)
		if err != nil {
			return stats, fmt.Errorf("%s: %w", t.name, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return stats, err
		}
		quoted := make([]string, len(cols))
		objCol := -1
		for i, c := range cols {
			quoted[i] = `"` + c + `"`
			if c == t.object {
				objCol = i
			}
		}
		ins := fmt.Sprintf(`INSERT INTO %s(%s) VALUES(%s)`, t.name, strings.Join(quoted, ","), strings.TrimSuffix(strings.Repeat("?,", len(cols)), ","))
		dtx, err := to.db.Begin()
		if err != nil {
			rows.Close()
			return stats, err
		}
		bc := newBlobCopier(from.blobs, to.blobs, &stats)
		var count int64
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				dtx.Rollback()
				return stats, err
			}
			if objCol >= 0 {
				bc.copy(asString(vals[objCol]))
			}
			// The SQLite driver reports an empty BLOB as []byte(nil); passing
			// that as a parameter turns it into NULL, which violates NOT NULL.
			for i := range vals {
				if b, ok := vals[i].([]byte); ok && b == nil {
					vals[i] = []byte{}
				}
			}
			if _, err := dtx.Exec(ins, vals...); err != nil {
				rows.Close()
				dtx.Rollback()
				return stats, fmt.Errorf("%s: %w", t.name, err)
			}
			count++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			dtx.Rollback()
			return stats, err
		}
		if err := bc.wait(); err != nil {
			dtx.Rollback()
			return stats, fmt.Errorf("%s blobs: %w", t.name, err)
		}
		if err := dtx.Commit(); err != nil {
			return stats, err
		}
		stats.Rows += count
		if logf != nil {
			logf("%-20s %8d rows", t.name, count)
		}
	}
	if to.d == postgresDialect {
		for _, t := range []string{"users", "teams", "packages"} {
			if _, err := to.db.Exec(fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s','id'), COALESCE((SELECT MAX(id) FROM %s), 0) + 1, false)`, t, t)); err != nil {
				return stats, err
			}
		}
	}
	return stats, nil
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return fmt.Sprint(v)
}

// blobCopier copies blobs with bounded parallelism.
type blobCopier struct {
	from, to blobStore
	stats    *MigrateStats
	wg       sync.WaitGroup
	sem      chan struct{}
	mu       sync.Mutex
	err      error
}

func newBlobCopier(from, to blobStore, st *MigrateStats) *blobCopier {
	return &blobCopier{from: from, to: to, stats: st, sem: make(chan struct{}, 16)}
}

func (b *blobCopier) copy(name string) {
	b.wg.Add(1)
	b.sem <- struct{}{}
	go func() {
		defer func() { <-b.sem; b.wg.Done() }()
		data, err := b.from.get(name)
		if errors.Is(err, os.ErrNotExist) {
			atomic.AddInt64(&b.stats.MissingBlobs, 1)
			return
		}
		if err == nil {
			err = b.to.put(name, data)
		}
		if err != nil {
			b.mu.Lock()
			if b.err == nil {
				b.err = fmt.Errorf("%s: %w", name, err)
			}
			b.mu.Unlock()
			return
		}
		atomic.AddInt64(&b.stats.Blobs, 1)
		atomic.AddInt64(&b.stats.BlobBytes, int64(len(data)))
	}()
}

func (b *blobCopier) wait() error {
	b.wg.Wait()
	return b.err
}
