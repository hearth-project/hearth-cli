package server

import (
	"crypto/hmac"
	"encoding/json"
	"net/http"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

func validNewCredentials(n *api.NewCredentials) bool {
	return n != nil && n.KDF.Validate() == nil && len(n.Salt) >= 16 && len(n.AuthKey) == crypto.KeySize && len(n.EncVaultKey) > 0
}

func (s *Server) storeCredentials(w http.ResponseWriter, uid int64, n *api.NewCredentials, revokeAll bool) bool {
	kdf, _ := json.Marshal(n.KDF)
	salt := crypto.RandomBytes(16)
	if err := s.st.setCredentials(uid, string(kdf), n.Salt, salt, authHash(salt, n.AuthKey), n.EncVaultKey, revokeAll); err != nil {
		s.writeStoreErr(w, err)
		return false
	}
	return true
}

// handleChangePassword re-wraps the vault key under a new password. The old
// auth key is required even with a valid token, so a stolen token alone
// cannot lock the owner out.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req api.ChangePasswordRequest
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.st.userByID(userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if !hmac.Equal(authHash(u.AuthSalt, req.OldAuthKey), u.AuthHash) {
		writeErr(w, http.StatusUnauthorized, "current password is wrong")
		return
	}
	if !validNewCredentials(&req.New) {
		writeErr(w, http.StatusBadRequest, "invalid key material")
		return
	}
	if s.storeCredentials(w, u.ID, &req.New, false) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleRotateRecovery(w http.ResponseWriter, r *http.Request) {
	var req api.RotateRecoveryRequest
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.st.userByID(userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if !hmac.Equal(authHash(u.AuthSalt, req.AuthKey), u.AuthHash) {
		writeErr(w, http.StatusUnauthorized, "password is wrong")
		return
	}
	if len(req.EncVaultKeyRecovery) == 0 || len(req.EncVaultKeyRecovery) > 1024 || len(req.RecoveryAuth) != crypto.KeySize {
		writeErr(w, http.StatusBadRequest, "invalid key material")
		return
	}
	salt := crypto.RandomBytes(16)
	if _, err := s.st.db.Exec(`UPDATE users SET enc_vault_key_recovery=?, recovery_salt=?, recovery_hash=? WHERE id=?`,
		req.EncVaultKeyRecovery, salt, authHash(salt, req.RecoveryAuth), u.ID); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRecover lets the holder of the recovery key (a) fetch the vault key
// wrapped by it and (b) set a new password. Success revokes every token,
// since a forgotten password may also mean a compromised one.
func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	var req api.RecoveryRequest
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.st.userByEmail(normEmail(req.Email))
	if err != nil || len(u.RecoveryHash) == 0 || !hmac.Equal(authHash(u.RecoverySalt, req.RecoveryAuth), u.RecoveryHash) {
		writeErr(w, http.StatusUnauthorized, "invalid email or recovery key")
		return
	}
	if u.Status != "active" {
		writeErr(w, http.StatusForbidden, statusMessage(u.Status))
		return
	}
	if req.New == nil {
		writeJSON(w, http.StatusOK, api.RecoveryResponse{EncVaultKeyRecovery: u.EncVaultKeyRecovery})
		return
	}
	if !validNewCredentials(req.New) {
		writeErr(w, http.StatusBadRequest, "invalid key material")
		return
	}
	if s.storeCredentials(w, u.ID, req.New, true) {
		s.auth.purgeUser(u.ID) // every token was revoked
		w.WriteHeader(http.StatusNoContent)
	}
}
