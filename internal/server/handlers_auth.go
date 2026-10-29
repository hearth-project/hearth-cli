package server

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxDevice
)

// loadOrCreateSecret returns the prelogin secret shared by all instances
// (stored in the database; a server.secret file from older versions is
// adopted on first start so fake salts stay stable).
func loadOrCreateSecret(st *store, dir string) ([]byte, error) {
	return st.setting("prelogin_secret", func() []byte {
		if b, err := os.ReadFile(filepath.Join(dir, "server.secret")); err == nil && len(b) == crypto.KeySize {
			return b
		}
		return crypto.RandomBytes(crypto.KeySize)
	})
}

func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	m.HandleFunc("GET /{$}", s.handleLanding)
	m.HandleFunc("GET /install.sh", s.handleInstall)
	m.HandleFunc("GET /ca.pem", s.handleCA)
	m.HandleFunc("GET /download/{name}", s.handleDownload)
	m.HandleFunc("GET /v1/dist", s.handleDistManifest)
	m.HandleFunc("GET /v1/info", s.handleInfo)
	m.HandleFunc("GET /onboard/SKILL.md", s.handleOnboardSkill)
	m.HandleFunc("POST /v1/auth/prelogin", s.limited(s.handlePrelogin))
	m.HandleFunc("POST /v1/auth/register", s.limited(s.handleRegister))
	m.HandleFunc("POST /v1/auth/login", s.limited(s.handleLogin))
	m.HandleFunc("POST /v1/auth/identity/login", s.limited(s.handleIdentityLogin))
	m.HandleFunc("POST /v1/auth/identity/enable", s.authed(s.handleIdentityEnable))
	m.HandleFunc("GET /v1/auth/identity", s.authed(s.handleIdentityStatus))
	m.HandleFunc("DELETE /v1/auth/identity", s.authed(s.handleIdentityDisable))
	m.HandleFunc("POST /v1/auth/recover", s.limited(s.handleRecover))
	m.HandleFunc("POST /v1/auth/password", s.authed(s.handleChangePassword))
	m.HandleFunc("POST /v1/auth/recovery-key", s.authed(s.handleRotateRecovery))

	m.HandleFunc("GET /v1/devices", s.authed(s.handleListDevices))
	m.HandleFunc("GET /v1/account/usage", s.authed(s.handleAccountUsage))
	m.HandleFunc("DELETE /v1/devices/{id}", s.authed(s.handleRevokeDevice))
	// The heartbeat answers how many console changes wait for this user.
	m.HandleFunc("POST /v1/devices/self/heartbeat", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var hb api.DeviceHeartbeat
		_ = json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&hb)
		machine, _ := json.Marshal(hb.Machine)
		health, _ := json.Marshal(hb.Health)
		if len(machine) > 64<<10 {
			machine = []byte("{}")
		}
		if len(health) > 64<<10 {
			health = []byte("{}")
		}
		if hb.Machine != nil || hb.Health != nil {
			_, _ = s.st.db.Exec(`UPDATE devices SET machine_json=?, health_json=?, health_at=? WHERE id=?`, machine, health, s.now(), deviceOf(r))
		}
		writeJSON(w, http.StatusOK, map[string]int{"intents": s.countIntents(userOf(r))})
	}))
	m.HandleFunc("GET /v1/intents", s.authed(s.handleListIntents))
	m.HandleFunc("DELETE /v1/intents/{id}", s.authed(s.handleDoneIntent))
	m.HandleFunc("POST /v1/devices/self/offline", s.authed(s.handleDeviceOffline))

	s.teamRoutes(m)
	s.adminRoutes(m)
	m.HandleFunc("GET /v1/secrets", s.authed(s.handleListSecrets))
	m.HandleFunc("PUT /v1/secrets/{id}", s.authed(s.handlePutSecret))
	m.HandleFunc("DELETE /v1/secrets/{id}", s.authed(s.handleDeleteSecret))
	m.HandleFunc("GET /v1/library", s.authed(s.handleGetLibrary))
	if s.cfg.SiteAuth {
		m.HandleFunc("GET /v1/site", s.authed(s.handleSite))
	} else {
		m.HandleFunc("GET /v1/site", s.handleSite) // needed before login (installer)
	}
	m.HandleFunc("PUT /v1/library", s.authed(s.handlePutLibrary))

	m.HandleFunc("GET /v1/sessions", s.authed(s.handleListSessions))
	m.HandleFunc("GET /v1/sessions/{sid}", s.authed(s.handleGetSession))
	m.HandleFunc("PUT /v1/sessions/{sid}", s.authed(s.handlePutSession))
	m.HandleFunc("DELETE /v1/sessions/{sid}", s.authed(s.handleDeleteSession))
	m.HandleFunc("POST /v1/sessions/{sid}/touch", s.authed(s.handleTouch))
	m.HandleFunc("POST /v1/sessions/{sid}/pin", s.authed(s.handlePin))
	m.HandleFunc("POST /v1/sessions/{sid}/expire", s.authed(s.handleExpire))
	m.HandleFunc("POST /v1/sessions/{sid}/revive", s.authed(s.handleRevive))
	m.HandleFunc("POST /v1/sessions/{sid}/reset", s.authed(s.handleReset))
	m.HandleFunc("POST /v1/sessions/{sid}/lease", s.authed(s.handleLease))
	m.HandleFunc("DELETE /v1/sessions/{sid}/lease", s.authed(s.handleRelease))
	m.HandleFunc("GET /v1/sessions/{sid}/chunks", s.authed(s.handleListChunks))
	m.HandleFunc("GET /v1/sessions/{sid}/content", s.authed(s.handleSessionContent))
	m.HandleFunc("PUT /v1/sessions/{sid}/chunks/{offset}", s.authed(s.handleAppendChunk))
	m.HandleFunc("GET /v1/sessions/{sid}/chunks/{offset}", s.authed(s.handleGetChunk))
	s.mux = m
}

// limited rate-limits unauthenticated endpoints per client IP.
func (s *Server) limited(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(clientIP(r), s.cfg.Now()) {
			writeErr(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		h(w, r)
	}
}

// authed requires a valid bearer token and validates {sid} if present.
func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			writeErr(w, http.StatusUnauthorized, "missing token")
			return
		}
		uid, did, err := s.auth.check(s, tokenHash(tok))
		if errors.Is(err, errNotFound) {
			writeErr(w, http.StatusUnauthorized, "invalid or expired token")
			return
		} else if err != nil {
			s.writeStoreErr(w, err)
			return
		}
		if sid := r.PathValue("sid"); sid != "" && !validSessionID(sid) {
			writeErr(w, http.StatusBadRequest, "invalid session id")
			return
		}
		s.activity.device(did, r.Header.Get(api.HeaderClientVersion), clientIP(r))
		ctx := context.WithValue(context.WithValue(r.Context(), ctxUser, uid), ctxDevice, did)
		h(w, r.WithContext(ctx))
	}
}

func userOf(r *http.Request) int64    { return r.Context().Value(ctxUser).(int64) }
func deviceOf(r *http.Request) string { return r.Context().Value(ctxDevice).(string) }

func (s *Server) handlePrelogin(w http.ResponseWriter, r *http.Request) {
	var req api.PreloginRequest
	if !readJSON(w, r, &req) {
		return
	}
	email := normEmail(req.Email)
	u, err := s.st.userByEmail(email)
	if errors.Is(err, errNotFound) {
		// Same shape as a real answer, so accounts cannot be enumerated.
		writeJSON(w, http.StatusOK, api.PreloginResponse{KDF: crypto.DefaultKDF, Salt: fakeSalt(s.secret, email)})
		return
	} else if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	var p crypto.KDFParams
	if err := json.Unmarshal([]byte(u.KDFParams), &p); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.PreloginResponse{KDF: p, Salt: u.KDFSalt})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req api.RegisterRequest
	if !readJSON(w, r, &req) {
		return
	}
	var inviteHash []byte
	mode := s.registration()
	switch mode {
	case RegistrationClosed:
		writeErr(w, http.StatusForbidden, "registration is closed on this server")
		return
	case RegistrationInvite:
		if strings.TrimSpace(req.InviteCode) == "" {
			writeErr(w, http.StatusForbidden, "registration requires an invite code")
			return
		}
		inviteHash = tokenHash(strings.TrimSpace(req.InviteCode))
	}
	email := normEmail(req.Email)
	if !strings.Contains(email, "@") || len(email) > 254 {
		writeErr(w, http.StatusBadRequest, "invalid email")
		return
	}
	if err := req.KDF.Validate(); err != nil || len(req.Salt) < 16 || len(req.AuthKey) != crypto.KeySize ||
		len(req.EncVaultKey) == 0 || len(req.EncVaultKeyRecovery) == 0 || len(req.RecoveryAuth) != crypto.KeySize {
		writeErr(w, http.StatusBadRequest, "invalid key material")
		return
	}
	kdf, _ := json.Marshal(req.KDF)
	authSalt, recSalt := crypto.RandomBytes(16), crypto.RandomBytes(16)
	status := "active"
	if mode == RegistrationApproval {
		status = "pending"
	}
	_, err := s.st.createUser(userRow{
		Status: status,
		Email:  email, KDFParams: string(kdf), KDFSalt: req.Salt, AuthSalt: authSalt,
		AuthHash: authHash(authSalt, req.AuthKey), EncVaultKey: req.EncVaultKey, EncVaultKeyRecovery: req.EncVaultKeyRecovery,
		RecoverySalt: recSalt, RecoveryHash: authHash(recSalt, req.RecoveryAuth),
	}, inviteHash, s.now())
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if status == "pending" {
		writeJSON(w, http.StatusCreated, map[string]string{"status": "pending"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req api.LoginRequest
	if !readJSON(w, r, &req) {
		return
	}
	email := normEmail(req.Email)
	u, err := s.st.userByEmail(email)
	if err != nil || !hmac.Equal(authHash(u.AuthSalt, req.AuthKey), u.AuthHash) {
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
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
	writeJSON(w, http.StatusOK, api.LoginResponse{Token: lr.Token, DeviceID: lr.DeviceID, ExpiresAt: lr.ExpiresAt, EncVaultKey: u.EncVaultKey})
}

var errBadDevice = errors.New("invalid device name")

type loginResult struct {
	Token     string
	DeviceID  string
	ExpiresAt int64
}

// issueLogin registers a device and returns a new bearer token.
func (s *Server) issueLogin(u *userRow, deviceName string) (loginResult, error) {
	name := strings.TrimSpace(deviceName)
	if name == "" || len(name) > 128 {
		return loginResult{}, errBadDevice
	}
	now := s.now()
	deviceID := hex.EncodeToString(crypto.RandomBytes(8))
	if err := s.st.createDevice(u.ID, deviceID, name, now); err != nil {
		return loginResult{}, err
	}
	tok := hex.EncodeToString(crypto.RandomBytes(32))
	exp := now + int64(s.cfg.TokenTTL.Seconds())
	if err := s.st.createToken(tokenHash(tok), u.ID, deviceID, exp); err != nil {
		return loginResult{}, err
	}
	return loginResult{Token: tok, DeviceID: deviceID, ExpiresAt: exp}, nil
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	ds, err := s.st.listDevices(userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	out := make([]api.Device, 0, len(ds))
	now := s.now()
	for _, d := range ds {
		online := d.Status == "active" && d.LastSeen >= now-int64(onlineWindow.Seconds()) && d.OfflineAt == 0
		var machine, health map[string]any
		_ = json.Unmarshal([]byte(d.MachineJSON), &machine)
		_ = json.Unmarshal([]byte(d.HealthJSON), &health)
		dev := api.Device{ID: d.ID, Name: d.Name, Status: d.Status, LastSeen: d.LastSeen, LastIP: d.LastIP, Online: online, Machine: machine, Health: health, HealthAt: d.HealthAt}
		if d.Status == "active" {
			b, _ := json.Marshal(s.machineEnv(d.HealthJSON, d.ClientVersion))
			_ = json.Unmarshal(b, &dev.Env)
		}
		out = append(out, dev)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.st.revokeDevice(userOf(r), r.PathValue("id")); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.auth.purgeUser(userOf(r))
	w.WriteHeader(http.StatusNoContent)
}

// statusMessage explains why an account that is not active cannot log in.
func statusMessage(status string) string {
	if status == "pending" {
		return "account waiting for the server admin's approval; try again once it is approved"
	}
	return "account disabled by the server admin"
}

// onlineWindow: a device counts as online when it was seen this recently.
// Daemons heartbeat every minute; last_seen is written at most every
// touchEvery (5 min), so the window allows for that.
const onlineWindow = 3 * touchEvery

// handleDeviceOffline: the daemon signs off on a graceful stop, so other
// machines restore its conversations right away. The next request from the
// device marks it online again.
func (s *Server) handleDeviceOffline(w http.ResponseWriter, r *http.Request) {
	did := deviceOf(r)
	if _, err := s.st.db.Exec(`UPDATE devices SET offline_at=? WHERE id=?`, s.now(), did); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.auth.retouchDevice(did)
	w.WriteHeader(http.StatusNoContent)
}
