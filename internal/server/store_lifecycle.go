package server

import (
	"database/sql"
	"errors"
)

// withSession runs fn in a transaction after loading the session row.
func (s *store) withSession(uid int64, sid string, fn func(tx *txx, r sessionRow) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := scanSession(tx.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE user_id=? AND session_id=?`+s.d.forUpdate(), uid, sid))
	if errors.Is(err, sql.ErrNoRows) {
		if s.tombstoned(tx, uid, sid) {
			return errGone
		}
		return errNotFound
	}
	if err != nil {
		return err
	}
	if err := fn(tx, r); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.flushDeletes()
}

// resetSession starts a new generation (used when the local file was
// rewritten or truncated) and drops all chunks of older generations.
func (s *store) resetSession(uid int64, sid, device string, now int64) (int64, error) {
	var gen int64
	err := s.withSession(uid, sid, func(tx *txx, r sessionRow) error {
		if leaseHeldByOther(r, device, now) {
			return errLocked
		}
		gen = r.Generation + 1
		if err := queueChunkDeletes(tx, `user_id=? AND session_id=?`, uid, sid); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE sessions SET generation=?, size_bytes=0, last_accessed_at=?, updated_at=?, status='active', expired_at=NULL
			WHERE user_id=? AND session_id=?`, gen, now, now, uid, sid)
		return err
	})
	return gen, err
}

// acquireLease grants or renews the single-writer lease.
func (s *store) acquireLease(uid int64, sid, device string, ttl int64, force bool, now int64) (sessionRow, error) {
	var out sessionRow
	err := s.withSession(uid, sid, func(tx *txx, r sessionRow) error {
		out = r
		if leaseHeldByOther(r, device, now) && !force {
			return errLocked
		}
		out.LeaseDevice, out.LeaseExpiresAt = device, now+ttl
		_, err := tx.Exec(`UPDATE sessions SET lease_device=?, lease_expires_at=?, last_accessed_at=?, status='active', expired_at=NULL
			WHERE user_id=? AND session_id=?`, device, now+ttl, now, uid, sid)
		return err
	})
	return out, err
}

func (s *store) releaseLease(uid int64, sid, device string) error {
	_, err := s.db.Exec(`UPDATE sessions SET lease_device=NULL, lease_expires_at=NULL
		WHERE user_id=? AND session_id=? AND lease_device=?`, uid, sid, device)
	return err
}

func (s *store) setPinned(uid int64, sid string, pinned bool, now int64) error {
	return s.withSession(uid, sid, func(tx *txx, _ sessionRow) error {
		_, err := tx.Exec(`UPDATE sessions SET pinned=?, last_accessed_at=?, status='active', expired_at=NULL
			WHERE user_id=? AND session_id=?`, boolInt(pinned), now, uid, sid)
		return err
	})
}

// expireSession marks a session expired now (unpinned, lease released), as
// the lifecycle GC does for unused sessions; GC deletes it after the grace
// period. Any later access (touch, restore, append) revives it.
func (s *store) expireSession(uid int64, sid string, now int64) error {
	return s.withSession(uid, sid, func(tx *txx, r sessionRow) error {
		if r.Status == "expired" {
			return nil
		}
		_, err := tx.Exec(`UPDATE sessions SET status='expired', expired_at=?, pinned=0, lease_device=NULL, lease_expires_at=NULL
			WHERE user_id=? AND session_id=?`, now, uid, sid)
		return err
	})
}

// deleteSession removes all data and leaves a tombstone so devices that
// still hold a local copy stop re-uploading it.
func (s *store) deleteSession(uid int64, sid string, now int64) error {
	return s.withSession(uid, sid, func(tx *txx, _ sessionRow) error {
		return deleteSessionTx(tx, uid, sid, now)
	})
}

func deleteSessionTx(tx *txx, uid int64, sid string, now int64) error {
	if err := queueChunkDeletes(tx, `user_id=? AND session_id=?`, uid, sid); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=? AND session_id=?`, uid, sid); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO session_tombstones(user_id,session_id,deleted_at) VALUES(?,?,?)
		ON CONFLICT(user_id,session_id) DO UPDATE SET deleted_at=excluded.deleted_at`, uid, sid, now)
	return err
}

// revive removes a tombstone so the session may be uploaded again.
func (s *store) revive(uid int64, sid string) error {
	_, err := s.db.Exec(`DELETE FROM session_tombstones WHERE user_id=? AND session_id=?`, uid, sid)
	return err
}

const pgGCLock = 0x61676763 // "aggc"

// gcStats summarises one expiry pass.
type gcStats struct {
	Expired, Deleted, TombstonesPurged int64
	DevicesRemoved                     int64
	users                              []int64 // whose devices were removed (auth cache)
}

// runGC applies the expiry policy from the design doc (section 6.8):
// active -> expired after expireAfter without access (unless pinned or
// leased), expired -> deleted after grace, tombstones purged after tombTTL.
func (s *store) runGC(now, expireAfter, grace, tombTTL, deviceTTL int64) (gcStats, error) {
	var st gcStats
	tx, err := s.db.Begin()
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	if s.d == postgresDialect {
		// Several instances share the database: one GC pass at a time.
		var got bool
		if err := tx.QueryRow(`SELECT pg_try_advisory_xact_lock(?)`, pgGCLock).Scan(&got); err != nil || !got {
			return st, err
		}
	}
	res, err := tx.Exec(`UPDATE sessions SET status='expired', expired_at=?
		WHERE status='active' AND pinned=0 AND last_accessed_at < ? AND COALESCE(lease_expires_at,0) <= ?`,
		now, now-expireAfter, now)
	if err != nil {
		return st, err
	}
	st.Expired, _ = res.RowsAffected()

	rows, err := tx.Query(`SELECT user_id, session_id FROM sessions WHERE status='expired' AND pinned=0 AND expired_at < ?`, now-grace)
	if err != nil {
		return st, err
	}
	type key struct {
		uid int64
		sid string
	}
	var victims []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.uid, &k.sid); err != nil {
			rows.Close()
			return st, err
		}
		victims = append(victims, k)
	}
	rows.Close()
	for _, k := range victims {
		if err := deleteSessionTx(tx, k.uid, k.sid, now); err != nil {
			return st, err
		}
	}
	st.Deleted = int64(len(victims))

	res, err = tx.Exec(`DELETE FROM session_tombstones WHERE deleted_at < ?`, now-tombTTL)
	if err != nil {
		return st, err
	}
	st.TombstonesPurged, _ = res.RowsAffected()

	// Machines not seen for deviceTTL (offline, reclaimed, revoked) are
	// forgotten: their row, health report and logins. A machine that comes
	// back logs in again.
	if deviceTTL > 0 {
		cut := now - deviceTTL
		rows, err := tx.Query(`SELECT DISTINCT user_id FROM devices WHERE last_seen < ?`, cut)
		if err != nil {
			return st, err
		}
		for rows.Next() {
			var uid int64
			if rows.Scan(&uid) == nil {
				st.users = append(st.users, uid)
			}
		}
		rows.Close()
		if _, err := tx.Exec(`DELETE FROM tokens WHERE device_id IN (SELECT id FROM devices WHERE last_seen < ?)`, cut); err != nil {
			return st, err
		}
		res, err = tx.Exec(`DELETE FROM devices WHERE last_seen < ?`, cut)
		if err != nil {
			return st, err
		}
		st.DevicesRemoved, _ = res.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}
	return st, s.flushDeletes()
}
