package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// One-time install commands. A signed-in user asks the web console for an
// install command; the console (which has the vault key from the password
// typed into it) wraps the vault key with a key derived from a random
// secret and sends the server only the ciphertext and the SHA-256 of the
// redeem proof. The secret travels in the command, so the new machine logs
// in and unlocks the vault without the master password, and the server
// still never holds a key that decrypts anything.

const (
	installTokenTTL = 30 * time.Minute
	// maxInstallTokens bounds the unused commands per user.
	maxInstallTokens = 10
)

var installIDRE = regexp.MustCompile(`^[0-9a-f]{16}$`)

func installAuthHash(auth []byte) []byte {
	h := sha256.Sum256(auth)
	return h[:]
}

// apiCreateInstallToken stores a wrapped vault key for one install command.
// Body: {"auth_hash": sha256(InstallAuth(secret)), "enc_vault_key": WrapWithInstall(...)}.
func (s *Server) apiCreateInstallToken(w http.ResponseWriter, r *http.Request, c userCtx) {
	var req struct {
		AuthHash    []byte `json:"auth_hash"`
		EncVaultKey []byte `json:"enc_vault_key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.AuthHash) != sha256.Size || len(req.EncVaultKey) < 24+16 || len(req.EncVaultKey) > 1024 {
		writeErr(w, http.StatusBadRequest, "invalid key material")
		return
	}
	now := s.now()
	if _, err := s.st.db.Exec(`DELETE FROM install_tokens WHERE user_id=? AND expires_at<?`, c.uid, now); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	var n int
	if err := s.st.db.QueryRow(`SELECT COUNT(*) FROM install_tokens WHERE user_id=?`, c.uid).Scan(&n); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if n >= maxInstallTokens {
		writeErr(w, http.StatusTooManyRequests, "too many unused install commands; they expire 30 minutes after they were made")
		return
	}
	id := hex.EncodeToString(crypto.RandomBytes(8))
	exp := now + int64(installTokenTTL.Seconds())
	if _, err := s.st.db.Exec(`INSERT INTO install_tokens(id,user_id,auth_hash,enc_vault_key,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
		id, c.uid, req.AuthHash, req.EncVaultKey, now, exp); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "email": c.email, "expires_at": exp})
}

// apiUnlockVault hands the console the password-wrapped vault key (a
// session opened with a link, or one whose browser lost its sealed copy),
// once it proves the password with the login key, and the session's wrap
// key. Body: {"auth_key": ...}.
func (s *Server) apiUnlockVault(w http.ResponseWriter, r *http.Request, c userCtx) {
	var req struct {
		AuthKey []byte `json:"auth_key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.st.userByID(c.uid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if !hmac.Equal(authHash(u.AuthSalt, req.AuthKey), u.AuthHash) {
		writeErr(w, http.StatusForbidden, "wrong password") // not 401: the console treats that as a lost session
		return
	}
	out := map[string]any{"enc_vault_key": u.EncVaultKey}
	if ck, err := r.Cookie(adminCookie); err == nil {
		out["wrap_key"] = s.consoleWrapKey(ck.Value)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleInstallLogin redeems an install command: the token is deleted
// whether or not the device login succeeds afterwards, so it works once.
func (s *Server) handleInstallLogin(w http.ResponseWriter, r *http.Request) {
	var req api.InstallLoginRequest
	if !readJSON(w, r, &req) {
		return
	}
	const bad = "this install command was used already or has expired; make a new one in the web console (Machines)"
	if !installIDRE.MatchString(req.ID) {
		writeErr(w, http.StatusUnauthorized, bad)
		return
	}
	var uid, exp int64
	var hash, wrapped []byte
	err := s.st.db.QueryRow(`SELECT user_id, auth_hash, enc_vault_key, expires_at FROM install_tokens WHERE id=?`, req.ID).Scan(&uid, &hash, &wrapped, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusUnauthorized, bad)
		return
	} else if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if !hmac.Equal(installAuthHash(req.Auth), hash) {
		writeErr(w, http.StatusUnauthorized, bad)
		return
	}
	u, err := s.st.userByID(uid)
	if err != nil || u.Email != normEmail(req.Email) {
		writeErr(w, http.StatusUnauthorized, bad)
		return
	}
	// Whoever deletes the row redeems it; a second use finds nothing.
	res, err := s.st.db.Exec(`DELETE FROM install_tokens WHERE id=?`, req.ID)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n != 1 || exp < s.now() {
		writeErr(w, http.StatusUnauthorized, bad)
		return
	}
	if u.Status != "active" {
		writeErr(w, http.StatusForbidden, statusMessage(u.Status))
		return
	}
	lr, err := s.issueLogin(u, req.DeviceName)
	if errors.Is(err, errBadDevice) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.InstallLoginResponse{Token: lr.Token, DeviceID: lr.DeviceID, ExpiresAt: lr.ExpiresAt, EncVaultKeyInstall: wrapped})
}
