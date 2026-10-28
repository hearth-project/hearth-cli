package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

var errIdentityDisabled = errors.New("platform identity login is not configured")

// identityUsername maps an account email to the platform (ERP) user name.
func identityUsername(email string) string {
	u, _, _ := strings.Cut(email, "@")
	return u
}

// brokerUserSK asks the platform's delegated-trust service for a user's
// secret key. The request is signed with the server-side system key; the
// platform secret never leaves this process.
func (s *Server) brokerUserSK(username string) ([]byte, error) {
	if s.cfg.IdentityBrokerURL == "" || s.cfg.IdentityBrokerKey == "" {
		return nil, errIdentityDisabled
	}
	ts := strconv.FormatInt(s.now(), 10)
	mac := hmac.New(sha256.New, []byte(s.cfg.IdentityBrokerKey))
	mac.Write([]byte(ts))
	token := ts + "/" + hex.EncodeToString(mac.Sum(nil))
	u := strings.TrimRight(s.cfg.IdentityBrokerURL, "/") +
		"/v3/user/userAuth?userName=" + url.QueryEscape(username)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("identity broker: status %d", resp.StatusCode)
	}
	var out struct {
		Code int `json:"code"`
		Data struct {
			SK string `json:"sk"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Code != 0 || out.Data.SK == "" {
		return nil, errors.New("identity broker returned no secret")
	}
	return []byte(out.Data.SK), nil
}

// handleIdentityEnable stores a vault key wrapped by the platform-identity
// key of the logged-in user. The server fetches the platform secret itself,
// so it can verify the same key on every future login.
func (s *Server) handleIdentityEnable(w http.ResponseWriter, r *http.Request) {
	var req api.IdentityEnableRequest
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.EncVaultKeyIdentity) == 0 || len(req.EncVaultKeyIdentity) > 1024 {
		writeErr(w, http.StatusBadRequest, "invalid key material")
		return
	}
	u, err := s.st.userByID(userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if u.IdentityDisabled != 0 && !req.Force {
		writeErr(w, http.StatusConflict, "identity login disabled by the user; run hearth identity enable to re-enable")
		return
	}
	sk, err := s.brokerUserSK(identityUsername(u.Email))
	switch {
	case errors.Is(err, errIdentityDisabled):
		writeErr(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusBadGateway, "platform identity is unavailable")
		return
	}
	salt := crypto.RandomBytes(16)
	_, err = s.st.db.Exec(`UPDATE users SET enc_vault_key_identity=?, identity_salt=?, identity_hash=?, identity_disabled=0 WHERE id=?`,
		req.EncVaultKeyIdentity, salt, authHash(salt, crypto.IdentityAuth(crypto.IdentityKey(sk))), u.ID)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleIdentityDisable(w http.ResponseWriter, r *http.Request) {
	if _, err := s.st.db.Exec(`UPDATE users SET enc_vault_key_identity=?, identity_salt=?, identity_hash=?, identity_disabled=1 WHERE id=?`,
		[]byte{}, []byte{}, []byte{}, userOf(r)); err != nil { // parameters: x'' is no bytea literal in PostgreSQL
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleIdentityStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.st.userByID(userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.IdentityStatusResponse{
		Enabled:  len(u.IdentityHash) > 0 && len(u.EncVaultKeyIdentity) > 0,
		Disabled: u.IdentityDisabled != 0,
	})
}

// handleIdentityLogin is passwordless login: the client proves possession
// of the platform secret the broker holds for this account.
func (s *Server) handleIdentityLogin(w http.ResponseWriter, r *http.Request) {
	var req api.IdentityLoginRequest
	if !readJSON(w, r, &req) {
		return
	}
	email := normEmail(req.Email)
	u, err := s.st.userByEmail(email)
	if err != nil || len(u.IdentityHash) == 0 {
		writeErr(w, http.StatusUnauthorized, "identity login is not enabled")
		return
	}
	if u.Status != "active" {
		writeErr(w, http.StatusForbidden, statusMessage(u.Status))
		return
	}
	if d := s.now() - req.Timestamp; d > 120 || d < -120 {
		writeErr(w, http.StatusUnauthorized, "stale identity proof")
		return
	}
	sk, err := s.brokerUserSK(identityUsername(email))
	if errors.Is(err, errIdentityDisabled) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "platform identity is unavailable")
		return
	}
	want := crypto.IdentityProof(crypto.IdentityKey(sk), api.IdentityLoginMessage(email, req.Timestamp))
	if !hmac.Equal(want, req.Proof) {
		writeErr(w, http.StatusUnauthorized, "invalid identity proof")
		return
	}
	lr, err := s.issueLogin(u, req.DeviceName)
	if err != nil {
		if errors.Is(err, errBadDevice) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.IdentityLoginResponse{
		Token: lr.Token, DeviceID: lr.DeviceID, ExpiresAt: lr.ExpiresAt,
		EncVaultKeyIdentity: u.EncVaultKeyIdentity,
	})
}
