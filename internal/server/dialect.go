package server

import (
	"database/sql"
	"strconv"
	"strings"
)

// The store is written in the SQL subset shared by SQLite (3.35+) and
// PostgreSQL: "?" placeholders (rewritten to $n for PostgreSQL),
// INSERT ... ON CONFLICT, RETURNING, and "offset" quoted. Everything
// dialect specific lives in this file and in the two schema files.

type dialect int

const (
	sqliteDialect dialect = iota
	postgresDialect
)

// rebind rewrites ? placeholders outside string literals to $1..$n.
func (d dialect) rebind(q string) string {
	if d != postgresDialect || !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 16)
	n, inStr := 0, false
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '\'':
			inStr = !inStr
		case c == '?' && !inStr:
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// forUpdate locks the selected rows until commit. SQLite needs nothing:
// write transactions start with BEGIN IMMEDIATE on the single writer.
func (d dialect) forUpdate() string {
	if d == postgresDialect {
		return " FOR UPDATE"
	}
	return ""
}

// isUnique reports a unique-constraint violation in either database.
func isUnique(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "UNIQUE constraint") || strings.Contains(m, "SQLSTATE 23505")
}

// isRetryable reports a PostgreSQL serialization failure or deadlock.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "SQLSTATE 40001") || strings.Contains(m, "SQLSTATE 40P01")
}

// dbx is a *sql.DB whose queries are rebound for the dialect.
type dbx struct {
	*sql.DB
	d dialect
}

func (x *dbx) Exec(q string, args ...any) (sql.Result, error) {
	return x.DB.Exec(x.d.rebind(q), args...)
}
func (x *dbx) Query(q string, args ...any) (*sql.Rows, error) {
	return x.DB.Query(x.d.rebind(q), args...)
}
func (x *dbx) QueryRow(q string, args ...any) *sql.Row { return x.DB.QueryRow(x.d.rebind(q), args...) }

func (x *dbx) Begin() (*txx, error) {
	t, err := x.DB.Begin()
	if err != nil {
		return nil, err
	}
	return &txx{Tx: t, d: x.d}, nil
}

// txx is a *sql.Tx whose queries are rebound for the dialect.
type txx struct {
	*sql.Tx
	d dialect
}

func (x *txx) Exec(q string, args ...any) (sql.Result, error) {
	return x.Tx.Exec(x.d.rebind(q), args...)
}
func (x *txx) Query(q string, args ...any) (*sql.Rows, error) {
	return x.Tx.Query(x.d.rebind(q), args...)
}
func (x *txx) QueryRow(q string, args ...any) *sql.Row { return x.Tx.QueryRow(x.d.rebind(q), args...) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
