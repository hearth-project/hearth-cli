// Package memory is the local long-term memory index: conversation cards
// of every machine, the user's remembered facts and the agents' own
// memory notes, in one SQLite FTS5 file per machine. Searches run on the
// agent's critical path (prompt hooks), so they only read this file: no
// network, no decryption, a few milliseconds.
package memory

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hearth-project/hearth-cli/internal/search"
)

// Doc is one searchable memory.
type Doc struct {
	ID      string `json:"id"`   // "s:<session id>", "f:<fact id>", "n:<session id>"
	Kind    string `json:"kind"` // conversation | fact | note
	Agent   string `json:"agent,omitempty"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Machine string `json:"machine,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	At      int64  `json:"at"`            // last activity (unix)
	Ref     string `json:"ref,omitempty"` // the agent's own file name (to tell a hook its current conversation)
}

// Hit is a search result.
type Hit struct {
	Doc
	Score float64 `json:"score"`
}

// Index is an open index file.
type Index struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS docs(
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, agent TEXT NOT NULL DEFAULT '', title TEXT NOT NULL,
  body TEXT NOT NULL, machine TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL DEFAULT 0,
  hash TEXT NOT NULL DEFAULT '', ref TEXT NOT NULL DEFAULT '');
CREATE VIRTUAL TABLE IF NOT EXISTS fts USING fts5(t, b, tokenize = 'unicode61 remove_diacritics 0');
CREATE VIRTUAL TABLE IF NOT EXISTS fts_vocab USING fts5vocab(fts, 'row');
CREATE TABLE IF NOT EXISTS kv(k TEXT PRIMARY KEY, v TEXT NOT NULL);`

// Path is the index file of a Hearth home.
func Path(home string) string { return filepath.Join(home, "memory", "index.db") }

// Open opens (creating) the index for writing.
func Open(path string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory index: %w", err)
	}
	os.Chmod(path, 0o600)
	return &Index{db: db}, nil
}

// OpenReadOnly opens an existing index for searching only; a missing file
// is os.ErrNotExist (nothing indexed yet).
func OpenReadOnly(path string) (*Index, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(1000)&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &Index{db: db}, nil
}

func (x *Index) Close() error { return x.db.Close() }

// tokens joins the shared tokenizer's output (lower-cased words and CJK
// bigrams) so FTS5's unicode61 sees one token per entry.
func tokens(s string) string { return strings.Join(search.Tokens(s), " ") }

// Hashes returns the stored content hash of every doc (for incremental
// updates).
func (x *Index) Hashes() (map[string]string, error) {
	rows, err := x.db.Query(`SELECT id, hash FROM docs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, h string
		if err := rows.Scan(&id, &h); err != nil {
			return nil, err
		}
		out[id] = h
	}
	return out, rows.Err()
}

// Apply upserts docs (with their content hashes) and deletes ids, in one
// transaction.
func (x *Index) Apply(put []Doc, hashes map[string]string, del []string) error {
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	remove := func(id string) error {
		var rowid int64
		err := tx.QueryRow(`SELECT rowid FROM docs WHERE id=?`, id).Scan(&rowid)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM fts WHERE rowid=?`, rowid); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM docs WHERE rowid=?`, rowid)
		return err
	}
	for _, id := range del {
		if err := remove(id); err != nil {
			return err
		}
	}
	for _, d := range put {
		if err := remove(d.ID); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO docs(id,kind,agent,title,body,machine,cwd,at,hash,ref) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			d.ID, d.Kind, d.Agent, d.Title, d.Body, d.Machine, d.Cwd, d.At, hashes[d.ID], d.Ref)
		if err != nil {
			return err
		}
		rowid, _ := res.LastInsertId()
		if _, err := tx.Exec(`INSERT INTO fts(rowid,t,b) VALUES(?,?,?)`, rowid, tokens(d.Title), tokens(d.Body)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetMeta / Meta keep small values (last indexed time).
func (x *Index) SetMeta(k, v string) error {
	_, err := x.db.Exec(`INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

func (x *Index) Meta(k string) string {
	var v string
	_ = x.db.QueryRow(`SELECT v FROM kv WHERE k=?`, k).Scan(&v)
	return v
}

// Count is the number of indexed docs per kind.
func (x *Index) Count() (map[string]int, error) {
	rows, err := x.db.Query(`SELECT kind, COUNT(*) FROM docs GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// Query is a search.
type Query struct {
	Text  string
	Limit int
	Cwd   string   // boosts memories of the same directory tree
	Kinds []string // only these kinds ("" = all)
	Now   time.Time
}

// Search ranks memories by BM25 over title (weight 3) and body, then by
// recency, kind (confirmed facts first) and directory.
func (x *Index) Search(q Query) ([]Hit, error) {
	toks := search.Tokens(q.Text)
	if len(toks) == 0 {
		return nil, nil
	}
	// Very common terms (in more than a fifth of all memories) cost the
	// most and rank the least: leave them out unless nothing else is left.
	var n int
	_ = x.db.QueryRow(`SELECT COUNT(*) FROM docs`).Scan(&n)
	seen := map[string]bool{}
	var terms, common []string
	for _, t := range toks {
		if seen[t] {
			continue
		}
		seen[t] = true
		quoted := `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
		var df int
		_ = x.db.QueryRow(`SELECT doc FROM fts_vocab WHERE term=?`, t).Scan(&df)
		switch {
		case df == 0:
		case n >= 50 && df*5 > n:
			common = append(common, quoted)
		default:
			terms = append(terms, quoted)
		}
	}
	join := " OR "
	if len(terms) == 0 {
		// Only common words: memories that have all of them.
		terms, join = common, " AND "
	}
	if len(terms) == 0 {
		return nil, nil
	}
	if q.Limit <= 0 {
		q.Limit = 5
	}
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	// Rank inside FTS5 first, then join the few best rows.
	sqlq := `SELECT d.id, d.kind, d.agent, d.title, d.body, d.machine, d.cwd, d.at, d.ref, m.r FROM
		(SELECT rowid, bm25(fts, 3.0, 1.0) AS r FROM fts WHERE fts MATCH ? ORDER BY r LIMIT ?) m
		JOIN docs d ON d.rowid = m.rowid`
	args := []any{strings.Join(terms, join), q.Limit * 12}
	if len(q.Kinds) > 0 {
		sqlq += ` WHERE d.kind IN (` + strings.TrimSuffix(strings.Repeat("?,", len(q.Kinds)), ",") + `)`
		for _, k := range q.Kinds {
			args = append(args, k)
		}
		args[1] = q.Limit * 40
	}
	rows, err := x.db.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		var h Hit
		var bm float64
		if err := rows.Scan(&h.ID, &h.Kind, &h.Agent, &h.Title, &h.Body, &h.Machine, &h.Cwd, &h.At, &h.Ref, &bm); err != nil {
			return nil, err
		}
		s := -bm // FTS5 bm25 is negative: lower is better
		age := q.Now.Sub(time.Unix(h.At, 0)).Hours() / 24
		if age < 0 {
			age = 0
		}
		s *= 1 / math.Sqrt(1+age/90)
		switch h.Kind {
		case "fact":
			s *= 1.6
		case "note":
			s *= 1.2
		}
		if q.Cwd != "" && h.Cwd != "" && (strings.HasPrefix(q.Cwd, h.Cwd) || strings.HasPrefix(h.Cwd, q.Cwd)) {
			s *= 1.3
		}
		h.Score = s
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	return hits, nil
}

// List returns docs of a kind, newest first.
func (x *Index) List(kind string, limit int) ([]Doc, error) {
	rows, err := x.db.Query(`SELECT id, kind, agent, title, body, machine, cwd, at, ref FROM docs WHERE kind=? ORDER BY at DESC LIMIT ?`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Doc
	for rows.Next() {
		var d Doc
		if err := rows.Scan(&d.ID, &d.Kind, &d.Agent, &d.Title, &d.Body, &d.Machine, &d.Cwd, &d.At, &d.Ref); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Get returns one doc.
func (x *Index) Get(id string) (Doc, bool) {
	var d Doc
	err := x.db.QueryRow(`SELECT id, kind, agent, title, body, machine, cwd, at, ref FROM docs WHERE id=?`, id).
		Scan(&d.ID, &d.Kind, &d.Agent, &d.Title, &d.Body, &d.Machine, &d.Cwd, &d.At, &d.Ref)
	return d, err == nil
}

// Matched counts the distinct query terms that occur in a doc.
func Matched(query string, d Doc) int {
	in := map[string]bool{}
	for _, t := range search.Tokens(d.Title + " " + d.Body) {
		in[t] = true
	}
	n, seen := 0, map[string]bool{}
	for _, t := range search.Tokens(query) {
		if !seen[t] {
			seen[t] = true
			if in[t] {
				n++
			}
		}
	}
	return n
}
