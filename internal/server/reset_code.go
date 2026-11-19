package server

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

const ResetCodeTTL = 30 * time.Minute

var errResetCode = errors.New("incorrect email or reset code, or the code has expired")

func (s *Server) handleRecoveryDelegation(w http.ResponseWriter, r *http.Request) {
	var req api.RecoveryDelegationRequest
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Envelope) > 1024 || (len(req.Envelope) > 0 && len(req.Envelope) < 80) {
		writeErr(w, http.StatusBadRequest, "invalid recovery envelope")
		return
	}
	uid := userOf(r)
	tx, err := s.st.db.Begin()
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	defer tx.Rollback()
	var hash, salt, passwordEnvelope []byte
	err = tx.QueryRow(`SELECT auth_hash,auth_salt,enc_vault_key FROM users WHERE id=? AND status='active'`+s.st.d.forUpdate(), uid).Scan(&hash, &salt, &passwordEnvelope)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.writeStoreErr(w, err)
		return
	}
	if err != nil || !hmac.Equal(authHash(salt, req.AuthKey), hash) {
		writeErr(w, http.StatusUnauthorized, "current password is wrong")
		return
	}
	if r.Method == http.MethodPost {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, api.ResetCodeResponse{Envelope: passwordEnvelope})
		return
	}
	// Replacing or withdrawing the envelope also cancels its outstanding grant.
	if _, err = tx.Exec(`DELETE FROM recovery_delegations WHERE user_id=?`, uid); err == nil && len(req.Envelope) > 0 {
		_, err = tx.Exec(`INSERT INTO recovery_delegations(user_id,envelope) VALUES(?,?)`, uid, req.Envelope)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// IssueResetCode is only called by the operator CLI with storage access and the
// separate private key. Issuance does not change the password or any device.
func (s *Server) IssueResetCode(email string, priv *ecdh.PrivateKey) (string, error) {
	email = normEmail(email)
	tx, err := s.st.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var uid int64
	var auth, delegated []byte
	err = tx.QueryRow(`SELECT u.id,u.auth_hash,d.envelope FROM users u JOIN recovery_delegations d ON d.user_id=u.id WHERE u.email=? AND u.status='active'`+s.st.d.forUpdate(), email).Scan(&uid, &auth, &delegated)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("no active account with administrator recovery enabled for this email")
	}
	if err != nil {
		return "", err
	}
	vk, err := crypto.OpenDelegatedVault(email, delegated, priv)
	if err != nil {
		return "", errors.New("the operator key does not open this account's recovery envelope")
	}
	defer clear(vk)
	if len(vk) != crypto.KeySize {
		return "", errors.New("invalid delegated vault key")
	}
	code := crypto.RandomBytes(crypto.KeySize)
	defer clear(code)
	wrapped, err := crypto.WrapForReset(email, vk, code)
	if err != nil {
		return "", err
	}
	proofHash := sha256.Sum256(crypto.ResetAuth(code))
	now := s.now()
	_, err = tx.Exec(`INSERT INTO password_reset_codes(user_id,proof_hash,envelope,issued_auth_hash,expires_at) VALUES(?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET claim_hash=NULL,proof_hash=excluded.proof_hash,envelope=excluded.envelope,issued_auth_hash=excluded.issued_auth_hash,expires_at=excluded.expires_at`, uid, proofHash[:], wrapped, auth, now+int64(ResetCodeTTL.Seconds()))
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(`INSERT INTO admin_audit(at,actor,action,target) VALUES(?,?,?,?)`, now, "cli", "issue-password-reset", email); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return crypto.EncodeRecoveryKey(code), nil
}

// Fetch the code-wrapped vault, then commit its new password wrapper. Lock
// and recheck everything at commit time, including expiration and supersession.
func (s *Server) handleResetCode(w http.ResponseWriter, r *http.Request) {
	var req api.ResetCodeRequest
	if !readJSON(w, r, &req) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if len(req.Proof) != crypto.KeySize || len(req.Claim) != crypto.KeySize {
		writeErr(w, http.StatusUnauthorized, errResetCode.Error())
		return
	}
	if req.New != nil && (!validNewCredentials(req.New) || len(req.New.EncVaultKey) != crypto.KeySize+24+16) {
		writeErr(w, http.StatusBadRequest, "invalid password material")
		return
	}
	wrapped, err := s.redeemResetCode(req)
	if errors.Is(err, errResetCode) {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if req.New == nil {
		writeJSON(w, http.StatusOK, api.ResetCodeResponse{Envelope: wrapped})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) redeemResetCode(req api.ResetCodeRequest) ([]byte, error) {
	tx, err := s.st.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	email := normEmail(req.Email)
	hash := sha256.Sum256(req.Proof)
	claim := sha256.Sum256(req.Claim)
	var uid int64
	var wrapped, claimed []byte
	err = tx.QueryRow(`SELECT u.id,c.envelope,c.claim_hash FROM users u JOIN password_reset_codes c ON u.id=c.user_id WHERE u.email=? AND u.status='active' AND u.auth_hash=c.issued_auth_hash AND c.proof_hash=? AND c.expires_at>?`+s.st.d.forUpdate(), email, hash[:], s.now()).Scan(&uid, &wrapped, &claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errResetCode
	}
	if err != nil {
		return nil, err
	}
	if len(claimed) > 0 && !hmac.Equal(claimed, claim[:]) {
		return nil, errResetCode
	}
	if req.New == nil {
		if _, err = tx.Exec(`UPDATE password_reset_codes SET claim_hash=? WHERE user_id=?`, claim[:], uid); err != nil {
			return nil, err
		}
		return wrapped, tx.Commit()
	}
	if len(claimed) == 0 {
		return nil, errResetCode
	}
	n := req.New
	kdf, _ := json.Marshal(n.KDF)
	salt := crypto.RandomBytes(16)
	if _, err = tx.Exec(`UPDATE users SET kdf_params=?,kdf_salt=?,auth_salt=?,auth_hash=?,enc_vault_key=? WHERE id=?`, string(kdf), n.Salt, salt, authHash(salt, n.AuthKey), n.EncVaultKey, uid); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`DELETE FROM password_reset_codes WHERE user_id=?`, uid); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO admin_audit(at,actor,action,target) VALUES(?,?,?,?)`, s.now(), "reset-code", "redeem-password-reset", email); err != nil {
		return nil, err
	}
	return nil, tx.Commit()
}
