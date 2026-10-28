package server

import (
	"database/sql"
	"errors"
	"fmt"
)

type sessionRow struct {
	ID             string `json:"id"`
	EncMeta        []byte `json:"enc_meta"`
	Generation     int64  `json:"generation"`
	Size           int64  `json:"size"`
	Status         string `json:"status"`
	Pinned         bool   `json:"pinned"`
	LastAccessedAt int64  `json:"last_accessed_at"`
	ExpiredAt      *int64 `json:"expired_at,omitempty"`
	LeaseDevice    string `json:"lease_device,omitempty"`
	LeaseExpiresAt int64  `json:"lease_expires_at,omitempty"`
	ActiveAt       int64  `json:"active_at,omitempty"`
	UpdatedBy      string `json:"updated_by,omitempty"`
}

type chunkRow struct {
	Generation int64  `json:"generation"`
	Offset     int64  `json:"offset"`
	Length     int64  `json:"length"`
	Object     string `json:"-"`
}

func (s *store) tombstoned(q interface {
	QueryRow(string, ...any) *sql.Row
}, uid int64, sid string) bool {
	var x int
	return q.QueryRow(`SELECT 1 FROM session_tombstones WHERE user_id=? AND session_id=?`, uid, sid).Scan(&x) == nil
}

// upsertSession registers a session or replaces its encrypted metadata.
func (s *store) upsertSession(uid int64, sid string, meta []byte, device string, now int64) error {
	if s.tombstoned(s.db, uid, sid) {
		return errGone
	}
	_, err := s.db.Exec(`INSERT INTO sessions(user_id,session_id,enc_meta,last_accessed_at,updated_at,active_at,updated_by) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(user_id,session_id) DO UPDATE SET enc_meta=excluded.enc_meta, updated_at=excluded.updated_at,
		updated_by=excluded.updated_by`, uid, sid, meta, now, now, now, device)
	return err
}

const sessionCols = `session_id,enc_meta,generation,size_bytes,status,pinned,last_accessed_at,expired_at,
	COALESCE(lease_device,''),COALESCE(lease_expires_at,0),active_at,COALESCE(updated_by,'')`

func scanSession(sc interface{ Scan(...any) error }) (sessionRow, error) {
	var r sessionRow
	var pinned int
	err := sc.Scan(&r.ID, &r.EncMeta, &r.Generation, &r.Size, &r.Status, &pinned, &r.LastAccessedAt, &r.ExpiredAt, &r.LeaseDevice, &r.LeaseExpiresAt, &r.ActiveAt, &r.UpdatedBy)
	r.Pinned = pinned != 0
	return r, err
}

// listSessions returns a user's sessions, most recently active first;
// limit <= 0 means all. total is the number of sessions the user has.
func (s *store) listSessions(uid int64, limit, offset int) (out []sessionRow, total int, err error) {
	if err := s.rdb.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, uid).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + sessionCols + ` FROM sessions WHERE user_id=? ORDER BY active_at DESC, session_id`
	args := []any{uid}
	if limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := s.rdb.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *store) getSession(uid int64, sid string) (sessionRow, error) {
	r, err := scanSession(s.rdb.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE user_id=? AND session_id=?`, uid, sid))
	if errors.Is(err, sql.ErrNoRows) {
		if s.tombstoned(s.rdb, uid, sid) {
			return r, errGone
		}
		return r, errNotFound
	}
	return r, err
}

// touch records an access, which also revives an expired session.
func (s *store) touch(uid int64, sid string, now int64) error {
	res, err := s.db.Exec(`UPDATE sessions SET last_accessed_at=?, status='active', expired_at=NULL WHERE user_id=? AND session_id=?`, now, uid, sid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err := s.getSession(uid, sid)
		return err
	}
	return nil
}

func leaseHeldByOther(r sessionRow, device string, now int64) bool {
	return r.LeaseDevice != "" && r.LeaseDevice != device && r.LeaseExpiresAt > now
}

// ConflictError reports the server's committed state for a session.
type conflictError struct{ Generation, Size int64 }

func (e *conflictError) Error() string {
	return fmt.Sprintf("conflict: generation=%d size=%d", e.Generation, e.Size)
}
func (e *conflictError) Unwrap() error { return errConflict }

// appendChunk commits one chunk at offset. Appends are idempotent: replaying
// an already committed (generation, offset, length) succeeds without change.
func (s *store) appendChunk(uid int64, sid, device string, gen, offset int64, length int64, data []byte, now int64) (sessionRow, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return sessionRow{}, err
	}
	defer tx.Rollback()
	r, err := scanSession(tx.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE user_id=? AND session_id=?`+s.d.forUpdate(), uid, sid))
	if errors.Is(err, sql.ErrNoRows) {
		if s.tombstoned(tx, uid, sid) {
			return r, errGone
		}
		return r, errNotFound
	}
	if err != nil {
		return r, err
	}
	if leaseHeldByOther(r, device, now) {
		return r, errLocked
	}
	if gen != r.Generation {
		return r, &conflictError{r.Generation, r.Size}
	}
	if offset < r.Size {
		var l int64
		err := tx.QueryRow(`SELECT length FROM session_chunks WHERE user_id=? AND session_id=? AND generation=? AND "offset"=?`, uid, sid, gen, offset).Scan(&l)
		if err == nil && l == length {
			return r, nil // replay of a committed chunk
		}
		return r, &conflictError{r.Generation, r.Size}
	}
	if offset > r.Size {
		return r, &conflictError{r.Generation, r.Size}
	}
	obj := fmt.Sprintf("%d/%s/%d-%d", uid, sid, gen, offset)
	// Object first, row second: a crash in between leaves an orphan file that
	// is overwritten by the retry of the same (generation, offset).
	if err := s.blobs.put(obj, data); err != nil {
		return r, err
	}
	if _, err := tx.Exec(`INSERT INTO session_chunks(user_id,session_id,generation,"offset",length,object) VALUES(?,?,?,?,?,?)`,
		uid, sid, gen, offset, length, obj); err != nil {
		return r, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET size_bytes=size_bytes+?, last_accessed_at=?, updated_at=?, active_at=?, updated_by=?, status='active', expired_at=NULL
		WHERE user_id=? AND session_id=?`, length, now, now, now, device, uid, sid); err != nil {
		return r, err
	}
	r.Size += length
	return r, tx.Commit()
}

func (s *store) listChunks(uid int64, sid string, gen int64) ([]chunkRow, error) {
	rows, err := s.rdb.Query(`SELECT generation,"offset",length,object FROM session_chunks
		WHERE user_id=? AND session_id=? AND generation=? ORDER BY "offset"`, uid, sid, gen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chunkRow
	for rows.Next() {
		var c chunkRow
		if err := rows.Scan(&c.Generation, &c.Offset, &c.Length, &c.Object); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *store) chunkObject(uid int64, sid string, gen, offset int64) (string, error) {
	var obj string
	err := s.rdb.QueryRow(`SELECT object FROM session_chunks WHERE user_id=? AND session_id=? AND generation=? AND "offset"=?`,
		uid, sid, gen, offset).Scan(&obj)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound
	}
	return obj, err
}
