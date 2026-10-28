package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
)

// Administration and storage quotas. Admin operations are methods on
// Server used by `hearth server admin ...`, which opens the data
// directory / database directly: like invites, only someone with access to
// the server's storage can administer it, and there is no admin HTTP API to
// protect. Other running instances notice disabled accounts within
// revalidateEvery (30 s) through the token cache.

var errQuota = errors.New("storage quota exceeded")

// UserUsage is one account as the admin sees it.
type UserUsage struct {
	Email        string    `json:"email"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	LastSeen     time.Time `json:"last_seen"`
	Devices      int64     `json:"devices"`
	Sessions     int64     `json:"sessions"`
	Expired      int64     `json:"expired_sessions"`
	SessionBytes int64     `json:"session_bytes"`
	LibraryBytes int64     `json:"library_bytes"`
	SecretBytes  int64     `json:"secret_bytes"`
	Secrets      int64     `json:"secrets"`
	Teams        []string  `json:"teams,omitempty"`
	QuotaBytes   int64     `json:"quota_bytes"` // effective; 0 = unlimited
	QuotaSource  string    `json:"quota_source"`
}

// Used is what counts against the quota.
func (u UserUsage) Used() int64 { return u.SessionBytes + u.LibraryBytes + u.SecretBytes }

// usageSQL aggregates per user in one pass (indexes: sessions and secrets
// are keyed by user_id first).
const usageSQL = `SELECT u.id, u.email, u.status, u.created_at, u.quota_bytes,
	COALESCE((SELECT MAX(last_seen) FROM devices d WHERE d.user_id=u.id), 0),
	(SELECT COUNT(*) FROM devices d WHERE d.user_id=u.id AND d.status='active'),
	(SELECT COUNT(*) FROM sessions s WHERE s.user_id=u.id),
	(SELECT COUNT(*) FROM sessions s WHERE s.user_id=u.id AND s.status='expired'),
	COALESCE((SELECT SUM(size_bytes) FROM sessions s WHERE s.user_id=u.id), 0),
	COALESCE((SELECT size_bytes FROM libraries l WHERE l.user_id=u.id), 0),
	(SELECT COUNT(*) FROM secrets x WHERE x.user_id=u.id),
	COALESCE((SELECT SUM(LENGTH(ciphertext)) FROM secrets x WHERE x.user_id=u.id), 0)
	FROM users u`

func (s *Server) scanUsage(rows *sql.Rows) (UserUsage, int64, error) {
	var u UserUsage
	var id, created, quota, last int64
	err := rows.Scan(&id, &u.Email, &u.Status, &created, &quota, &last, &u.Devices, &u.Sessions, &u.Expired,
		&u.SessionBytes, &u.LibraryBytes, &u.Secrets, &u.SecretBytes)
	u.CreatedAt, u.LastSeen = time.Unix(created, 0), time.Unix(last, 0)
	u.QuotaBytes, u.QuotaSource = s.effectiveQuota(quota)
	return u, id, err
}

func (s *Server) effectiveQuota(q int64) (int64, string) {
	if q < 0 {
		return s.defaultQuota(), "default"
	}
	return q, "user"
}

// Users lists every account with its usage, largest first.
func (s *Server) Users() ([]UserUsage, error) {
	rows, err := s.st.rdb.Query(usageSQL + ` ORDER BY u.email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserUsage
	for rows.Next() {
		u, _, err := s.scanUsage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Used() > out[j].Used() })
	return out, rows.Err()
}

// User returns one account with its usage and teams.
func (s *Server) User(email string) (UserUsage, error) {
	rows, err := s.st.rdb.Query(usageSQL+` WHERE u.email=?`, normEmail(email))
	if err != nil {
		return UserUsage{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return UserUsage{}, fmt.Errorf("%w: no user %s", errNotFound, email)
	}
	u, id, err := s.scanUsage(rows)
	if err != nil {
		return u, err
	}
	rows.Close()
	ts, err := s.st.listTeams(id)
	for _, t := range ts {
		u.Teams = append(u.Teams, t.Name+"("+t.Role+")")
	}
	return u, err
}

func (s *Server) uidOf(email string) (int64, error) {
	u, err := s.st.userByEmail(normEmail(email))
	if err != nil {
		return 0, fmt.Errorf("no user %s: %w", email, err)
	}
	return u.ID, nil
}

// SetUserStatus disables or re-enables an account. A disabled account
// cannot log in, recover or use its tokens; its data is kept.
func (s *Server) SetUserStatus(email string, active bool) error {
	uid, err := s.uidOf(email)
	if err != nil {
		return err
	}
	st := "disabled"
	if active {
		st = "active"
	}
	if _, err := s.st.db.Exec(`UPDATE users SET status=? WHERE id=?`, st, uid); err != nil {
		return err
	}
	s.auth.purgeUser(uid)
	return nil
}

// SetUserQuota sets a per-user quota: bytes >= 0 (0 = unlimited) or -1 to
// use the server default.
func (s *Server) SetUserQuota(email string, bytes int64) error {
	uid, err := s.uidOf(email)
	if err != nil {
		return err
	}
	if bytes < -1 {
		return fmt.Errorf("%w: quota must be >= 0 or -1 (default)", errBadRequest)
	}
	_, err = s.st.db.Exec(`UPDATE users SET quota_bytes=? WHERE id=?`, bytes, uid)
	s.quota.forget(uid)
	return err
}

// RevokeUserDevices revokes every device of an account (all tokens).
func (s *Server) RevokeUserDevices(email string) (int64, error) {
	uid, err := s.uidOf(email)
	if err != nil {
		return 0, err
	}
	tx, err := s.st.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE devices SET status='revoked' WHERE user_id=? AND status='active'`, uid)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM tokens WHERE user_id=?`, uid); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	s.auth.purgeUser(uid)
	return n, tx.Commit()
}

// DeleteUser removes an account and all its data (sessions, library,
// secrets, devices, memberships). Packages it published stay with their
// teams. Refused while the user is the only owner of a team.
func (s *Server) DeleteUser(email string) error {
	uid, err := s.uidOf(email)
	if err != nil {
		return err
	}
	tx, err := s.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT t.name FROM teams t JOIN team_members m ON m.team_id=t.id
		WHERE m.user_id=? AND m.role='owner' AND (SELECT COUNT(*) FROM team_members o WHERE o.team_id=t.id AND o.role='owner')=1`, uid)
	if err != nil {
		return err
	}
	var sole []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			sole = append(sole, n)
		}
	}
	rows.Close()
	if len(sole) > 0 {
		return fmt.Errorf("%w: %s is the only owner of team(s) %s; add another owner first (hearth team add)", errBadRequest, email, strings.Join(sole, ", "))
	}
	if err := queueChunkDeletes(tx, `user_id=?`, uid); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO pending_deletes(object) SELECT object FROM libraries WHERE user_id=? ON CONFLICT DO NOTHING`, uid); err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM session_tombstones WHERE user_id=?`, `DELETE FROM sessions WHERE user_id=?`,
		`DELETE FROM usage_daily WHERE user_id=?`, `DELETE FROM users WHERE id=?`} {
		if _, err := tx.Exec(q, uid); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.auth.purgeUser(uid)
	s.quota.forget(uid)
	return s.st.flushDeletes()
}

// InviteInfo is one invite code (only its hash is stored).
type InviteInfo struct {
	ID        string    `json:"id"` // first hex chars of the hash, for revoke
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UsedBy    string    `json:"used_by,omitempty"`
	hash      []byte
}

// Invites lists unused and used invites, newest first.
func (s *Server) Invites() ([]InviteInfo, error) {
	rows, err := s.st.rdb.Query(`SELECT i.code_hash, i.created_at, i.expires_at, COALESCE(u.email, '')
		FROM invites i LEFT JOIN users u ON u.id = i.used_by ORDER BY i.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InviteInfo
	for rows.Next() {
		var h []byte
		var c, e int64
		var used string
		if err := rows.Scan(&h, &c, &e, &used); err != nil {
			return nil, err
		}
		out = append(out, InviteInfo{ID: fmt.Sprintf("%x", h)[:12], CreatedAt: time.Unix(c, 0), ExpiresAt: time.Unix(e, 0), UsedBy: used, hash: h})
	}
	return out, rows.Err()
}

// RevokeInvite deletes unused invites whose id (hash prefix) matches.
func (s *Server) RevokeInvite(id string) error {
	invs, err := s.Invites()
	if err != nil {
		return err
	}
	if len(id) < 6 {
		return fmt.Errorf("%w: give at least 6 characters of the invite id", errBadRequest)
	}
	for _, in := range invs {
		if strings.HasPrefix(in.ID, strings.ToLower(id)) {
			if in.UsedBy != "" {
				return fmt.Errorf("%w: invite %s was already used by %s", errBadRequest, in.ID, in.UsedBy)
			}
			_, err := s.st.db.Exec(`DELETE FROM invites WHERE code_hash=? AND used_at IS NULL`, in.hash)
			return err
		}
	}
	return fmt.Errorf("%w: no invite %s", errNotFound, id)
}

// ---- quota enforcement ----

// quotaCache keeps each user's usage in memory so the hot write path does
// not aggregate on every chunk: it is loaded once, then advanced by the
// writes of this instance and re-read every quotaRefresh (other instances,
// GC, deletes). Quotas are therefore soft by at most one refresh period of
// another instance's writes. A write that would exceed the quota re-reads
// first, so raising a quota takes effect immediately.
type quotaCache struct {
	mu    sync.Mutex
	users map[int64]*quotaEntry
}

type quotaEntry struct {
	used, limit int64
	at          time.Time
}

const quotaRefresh = 30 * time.Second

func newQuotaCache() *quotaCache { return &quotaCache{users: map[int64]*quotaEntry{}} }

func (c *quotaCache) reset() {
	c.mu.Lock()
	c.users = map[int64]*quotaEntry{}
	c.mu.Unlock()
}

func (c *quotaCache) forget(uid int64) {
	c.mu.Lock()
	delete(c.users, uid)
	c.mu.Unlock()
}

func (s *Server) loadUsage(uid int64) (used, limit int64, err error) {
	var q, sess, lib, sec int64
	err = s.st.rdb.QueryRow(`SELECT quota_bytes,
		COALESCE((SELECT SUM(size_bytes) FROM sessions WHERE user_id=?), 0),
		COALESCE((SELECT size_bytes FROM libraries WHERE user_id=?), 0),
		COALESCE((SELECT SUM(LENGTH(ciphertext)) FROM secrets WHERE user_id=?), 0)
		FROM users WHERE id=?`, uid, uid, uid, uid).Scan(&q, &sess, &lib, &sec)
	limit, _ = s.effectiveQuota(q)
	return sess + lib + sec, limit, err
}

// reserve admits a write of delta bytes for uid, or returns errQuota.
// Shrinking writes (delta <= 0) are always admitted.
func (s *Server) reserve(uid, delta int64) error {
	if s.defaultQuota() == 0 && !s.hasUserQuotas() {
		return nil // no quotas anywhere: skip all bookkeeping
	}
	now := s.cfg.Now()
	c := s.quota
	c.mu.Lock()
	e := c.users[uid]
	c.mu.Unlock()
	if e == nil || now.Sub(e.at) > quotaRefresh {
		used, limit, err := s.loadUsage(uid)
		if err != nil {
			return err
		}
		e = &quotaEntry{used: used, limit: limit, at: now}
		c.mu.Lock()
		c.users[uid] = e
		c.mu.Unlock()
	}
	c.mu.Lock()
	over := delta > 0 && e.limit > 0 && e.used+delta > e.limit
	stale := now.Sub(e.at) > time.Second
	c.mu.Unlock()
	if over && stale {
		// Refusing is rare: re-read first, the admin may have raised the
		// quota or the user deleted data through another instance.
		used, limit, err := s.loadUsage(uid)
		if err != nil {
			return err
		}
		c.mu.Lock()
		e.used, e.limit, e.at = used, limit, now
		over = e.limit > 0 && e.used+delta > e.limit
		c.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if over {
		return fmt.Errorf("%w: %d of %d bytes used", errQuota, e.used, e.limit)
	}
	e.used += delta
	return nil
}

// hasUserQuotas reports whether any account has its own quota (cached).
func (s *Server) hasUserQuotas() bool {
	now := s.cfg.Now()
	s.quotaFlag.Lock()
	defer s.quotaFlag.Unlock()
	if now.Sub(s.quotaFlag.at) > quotaRefresh {
		var n int64
		s.st.rdb.QueryRow(`SELECT COUNT(*) FROM users WHERE quota_bytes > 0`).Scan(&n)
		s.quotaFlag.any, s.quotaFlag.at = n > 0, now
	}
	return s.quotaFlag.any
}

func (s *Server) handleAccountUsage(w http.ResponseWriter, r *http.Request) {
	email, err := s.st.emailOf(userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	u, err := s.User(email)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.AccountUsage{Used: u.Used(), Quota: u.QuotaBytes, Sessions: u.Sessions,
		SessionBytes: u.SessionBytes, LibraryBytes: u.LibraryBytes, SecretBytes: u.SecretBytes, Devices: u.Devices})
}

// ServerStats summarises the whole deployment.
type ServerStats struct {
	Users, DisabledUsers, ActiveUsers30d int64
	Devices, Sessions, ExpiredSessions   int64
	SessionBytes, LibraryBytes           int64
	Teams, Packages, PackageBytes        int64
	PendingDeletes                       int64
	Storage                              string
}

// Stats aggregates the deployment.
func (s *Server) Stats() (ServerStats, error) {
	st := ServerStats{Storage: s.Storage()}
	since := s.now() - 30*24*3600
	err := s.st.rdb.QueryRow(`SELECT
		(SELECT COUNT(*) FROM users), (SELECT COUNT(*) FROM users WHERE status<>'active'),
		(SELECT COUNT(DISTINCT user_id) FROM devices WHERE last_seen>=?),
		(SELECT COUNT(*) FROM devices WHERE status='active'),
		(SELECT COUNT(*) FROM sessions), (SELECT COUNT(*) FROM sessions WHERE status='expired'),
		COALESCE((SELECT SUM(size_bytes) FROM sessions), 0), COALESCE((SELECT SUM(size_bytes) FROM libraries), 0),
		(SELECT COUNT(*) FROM teams), (SELECT COUNT(*) FROM package_versions),
		COALESCE((SELECT SUM(size_bytes) FROM package_versions), 0), (SELECT COUNT(*) FROM pending_deletes)`, since).Scan(
		&st.Users, &st.DisabledUsers, &st.ActiveUsers30d, &st.Devices, &st.Sessions, &st.ExpiredSessions,
		&st.SessionBytes, &st.LibraryBytes, &st.Teams, &st.Packages, &st.PackageBytes, &st.PendingDeletes)
	return st, err
}

// SweepStats summarises an orphan sweep.
type SweepStats struct {
	Blobs, Referenced, Orphans, Deleted, Bytes int64 // Bytes: size of the orphans
}

// SweepOrphans deletes blobs no row refers to (left by crashes between a
// blob write and its commit, or by bugs). Blobs younger than minAge are
// kept: an upload may be about to commit them.
func (s *Server) SweepOrphans(minAge time.Duration, dryRun bool) (SweepStats, error) {
	var st SweepStats
	if err := s.st.flushDeletes(); err != nil {
		return st, err
	}
	refs := map[string]bool{}
	for _, q := range []string{`SELECT object FROM session_chunks`, `SELECT object FROM libraries`,
		`SELECT object FROM package_versions`, `SELECT object FROM pending_deletes`} {
		rows, err := s.st.rdb.Query(q)
		if err != nil {
			return st, err
		}
		for rows.Next() {
			var o string
			if rows.Scan(&o) == nil {
				refs[o] = true
			}
		}
		rows.Close()
	}
	st.Referenced = int64(len(refs))
	cutoff := s.cfg.Now().Add(-minAge)
	var orphans []string
	err := s.st.blobs.list(func(name string, mod time.Time, size int64) error {
		st.Blobs++
		if !refs[name] && mod.Before(cutoff) {
			orphans = append(orphans, name)
			st.Bytes += size
		}
		return nil
	})
	if err != nil {
		return st, err
	}
	st.Orphans = int64(len(orphans))
	if dryRun {
		return st, nil
	}
	for _, o := range orphans {
		if err := s.st.blobs.del(o); err != nil {
			return st, err
		}
		st.Deleted++
	}
	return st, nil
}
