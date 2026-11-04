package server

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
)

type userRow struct {
	ID                  int64
	Email               string
	KDFParams           string
	KDFSalt             []byte
	AuthSalt            []byte
	AuthHash            []byte
	EncVaultKey         []byte
	EncVaultKeyRecovery []byte
	RecoverySalt        []byte
	RecoveryHash        []byte // HMAC of the recovery-auth key; empty = recovery not set up
	EncVaultKeyIdentity []byte
	IdentitySalt        []byte
	IdentityHash        []byte // HMAC of the identity-auth key; empty = identity login not set up
	IdentityDisabled    int    // 1 = the user opted out; automatic enrollment must not re-enable it
	Status              string // active | disabled | pending (approval mode, until an admin approves)
}

// createUser inserts a user. If inviteHash is non-nil, the invite is
// consumed in the same transaction, so a code can register only one user.
func (s *store) createUser(u userRow, inviteHash []byte, now int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if inviteHash != nil {
		res, err := tx.Exec(`UPDATE invites SET used_at=? WHERE code_hash=? AND used_at IS NULL AND expires_at>?`, now, inviteHash, now)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return 0, errInvite
		}
	}
	var id int64
	if u.Status == "" {
		u.Status = "active"
	}
	err = tx.QueryRow(`INSERT INTO users(email,kdf_params,kdf_salt,auth_salt,auth_hash,enc_vault_key,enc_vault_key_recovery,recovery_salt,recovery_hash,created_at,status)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING id`, u.Email, u.KDFParams, u.KDFSalt, u.AuthSalt, u.AuthHash, u.EncVaultKey, u.EncVaultKeyRecovery, u.RecoverySalt, u.RecoveryHash, now, u.Status).Scan(&id)
	if err != nil {
		if isUnique(err) {
			return 0, errExists
		}
		return 0, err
	}
	if inviteHash != nil {
		if _, err := tx.Exec(`UPDATE invites SET used_by=? WHERE code_hash=?`, id, inviteHash); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func (s *store) createInvite(hash []byte, now, expires int64) error {
	_, err := s.db.Exec(`INSERT INTO invites(code_hash,created_at,expires_at) VALUES(?,?,?)`, hash, now, expires)
	return err
}

func (s *store) userByEmail(email string) (*userRow, error) {
	var u userRow
	err := s.rdb.QueryRow(`SELECT id,email,kdf_params,kdf_salt,auth_salt,auth_hash,enc_vault_key,enc_vault_key_recovery,recovery_salt,recovery_hash,
		enc_vault_key_identity,identity_salt,identity_hash,identity_disabled,status
		FROM users WHERE email = ?`, email).Scan(&u.ID, &u.Email, &u.KDFParams, &u.KDFSalt, &u.AuthSalt, &u.AuthHash, &u.EncVaultKey, &u.EncVaultKeyRecovery,
		&u.RecoverySalt, &u.RecoveryHash, &u.EncVaultKeyIdentity, &u.IdentitySalt, &u.IdentityHash, &u.IdentityDisabled, &u.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return &u, err
}

func (s *store) createDevice(userID int64, id, name string, now int64) error {
	_, err := s.db.Exec(`INSERT INTO devices(id,user_id,name,created_at,last_seen) VALUES(?,?,?,?,?)`, id, userID, name, now, now)
	return err
}

func (s *store) createToken(hash []byte, userID int64, deviceID string, expires int64) error {
	_, err := s.db.Exec(`INSERT INTO tokens(token_hash,user_id,device_id,expires_at) VALUES(?,?,?,?)`, hash, userID, deviceID, expires)
	return err
}

// lookupToken resolves a token hash to (user, device) if valid and the
// device has not been revoked.
func (s *store) lookupToken(hash []byte, now int64) (int64, string, error) {
	var uid int64
	var did string
	err := s.rdb.QueryRow(`SELECT t.user_id, t.device_id FROM tokens t JOIN devices d ON d.id = t.device_id
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ? AND t.expires_at > ? AND d.status = 'active' AND u.status = 'active'`, hash, now).Scan(&uid, &did)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", errNotFound
	}
	return uid, did, err
}

// touchToken slides the token expiry to now+ttl and records device
// activity. Called at most every few minutes per token (see authCache).
func (s *store) touchToken(hash []byte, did string, now, ttl int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tokens SET expires_at = ? WHERE token_hash = ?`, now+ttl, hash); err != nil {
		return err
	}
	// A request after signing off (daemon restarted) is online again.
	if _, err := tx.Exec(`UPDATE devices SET last_seen = ?, offline_at = 0 WHERE id = ?`, now, did); err != nil {
		return err
	}
	return tx.Commit()
}

type deviceRow struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	LastSeen      int64  `json:"last_seen"`
	OfflineAt     int64  `json:"-"`
	LastIP        string `json:"last_ip"`
	MachineJSON   string `json:"machine_json"`
	HealthJSON    string `json:"health_json"`
	HealthAt      int64  `json:"health_at"`
	ClientVersion string `json:"client_version"`
	Alias         string `json:"alias"`
}

func (s *store) listDevices(userID int64) ([]deviceRow, error) {
	rows, err := s.rdb.Query(`SELECT id,name,status,last_seen,offline_at,last_ip,machine_json,health_json,health_at,client_version,alias FROM devices WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []deviceRow
	for rows.Next() {
		var d deviceRow
		if err := rows.Scan(&d.ID, &d.Name, &d.Status, &d.LastSeen, &d.OfflineAt, &d.LastIP, &d.MachineJSON, &d.HealthJSON, &d.HealthAt, &d.ClientVersion, &d.Alias); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// aliasRE: a name people give a machine; also an SSH Host alias.
var aliasRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// errAliasTaken: another active machine of the user has the name.
var errAliasTaken = errors.New("another of your machines has that name")

// setDeviceAlias names a device of the user ("" = back to its host name).
func (s *store) setDeviceAlias(userID int64, id, alias string) error {
	if alias != "" && !aliasRE.MatchString(alias) {
		return fmt.Errorf("%w: a name is 1-63 letters, digits, '.', '_' or '-'", errBadRequest)
	}
	if alias != "" {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE user_id=? AND id<>? AND status='active' AND (alias=? OR (alias='' AND name=?))`, userID, id, alias, alias).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errAliasTaken
		}
	}
	res, err := s.db.Exec(`UPDATE devices SET alias=? WHERE user_id=? AND id=?`, alias, userID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

func (s *store) revokeDevice(userID int64, id string) error {
	res, err := s.db.Exec(`UPDATE devices SET status = 'revoked' WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	_, err = s.db.Exec(`DELETE FROM tokens WHERE device_id = ?`, id)
	return err
}

// errDeviceOnline: a machine that is still online cannot be replaced.
var errDeviceOnline = errors.New("that machine is online; only an offline machine can be replaced (remove it first if it is really gone)")

// replaceDevice lets device newID take over oldID, an offline machine of
// the same user (reinstalled, or a new machine given its name): newID gets
// alias, the conversations oldID wrote count as newID's, and oldID is
// removed (logged out, out of the list). alias "" keeps newID's own name.
func (s *store) replaceDevice(userID int64, newID, oldID, alias string, onlineSince int64) error {
	if newID == oldID {
		return fmt.Errorf("%w: a machine cannot replace itself", errBadRequest)
	}
	if alias != "" && !aliasRE.MatchString(alias) {
		return fmt.Errorf("%w: a name is 1-63 letters, digits, '.', '_' or '-'", errBadRequest)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	var lastSeen, offlineAt int64
	err = tx.QueryRow(`SELECT status,last_seen,offline_at FROM devices WHERE user_id=? AND id=?`, userID, oldID).Scan(&status, &lastSeen, &offlineAt)
	if errors.Is(err, sql.ErrNoRows) || err == nil && status != "active" {
		return errNotFound
	} else if err != nil {
		return err
	}
	if lastSeen >= onlineSince && offlineAt == 0 {
		return errDeviceOnline
	}
	if _, err := tx.Exec(`UPDATE devices SET status='revoked' WHERE user_id=? AND id=?`, userID, oldID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tokens WHERE device_id=?`, oldID); err != nil {
		return err
	}
	if alias != "" {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM devices WHERE user_id=? AND id<>? AND status='active' AND (alias=? OR (alias='' AND name=?))`, userID, newID, alias, alias).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errAliasTaken
		}
	}
	var mine int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM devices WHERE user_id=? AND id=? AND status='active'`, userID, newID).Scan(&mine); err != nil {
		return err
	}
	if mine == 0 {
		return errNotFound
	}
	if alias != "" {
		if _, err := tx.Exec(`UPDATE devices SET alias=? WHERE user_id=? AND id=?`, alias, userID, newID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE sessions SET updated_by=? WHERE user_id=? AND updated_by=?`, newID, userID, oldID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET lease_device=NULL, lease_expires_at=NULL WHERE user_id=? AND lease_device=?`, userID, oldID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *store) emailOf(uid int64) (string, error) {
	var e string
	err := s.rdb.QueryRow(`SELECT email FROM users WHERE id=?`, uid).Scan(&e)
	return e, err
}

func (s *store) userByID(uid int64) (*userRow, error) {
	e, err := s.emailOf(uid)
	if err != nil {
		return nil, err
	}
	return s.userByEmail(e)
}

// setCredentials replaces the password-derived material. With revokeAll,
// every token of the user is deleted (used by account recovery).
func (s *store) setCredentials(uid int64, kdf string, kdfSalt, authSalt, authHash, encVaultKey []byte, revokeAll bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET kdf_params=?, kdf_salt=?, auth_salt=?, auth_hash=?, enc_vault_key=? WHERE id=?`,
		kdf, kdfSalt, authSalt, authHash, encVaultKey, uid); err != nil {
		return err
	}
	if revokeAll {
		if _, err := tx.Exec(`DELETE FROM tokens WHERE user_id=?`, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}
