// Package server implements the hearth sync server. It stores only
// ciphertext produced by clients plus the minimum metadata needed for
// authentication, append ordering, leases and expiry.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// Registration modes.
const (
	RegistrationInvite = "invite" // default: a single-use invite code is required
	RegistrationOpen   = "open"   // anyone who can reach the server may register
	RegistrationClosed = "closed" // no new accounts
	// RegistrationApproval ("approval", settings.go): sign-up is open, an
	// admin approves each account in the panel.
)

// Config controls server behaviour. Zero values fall back to defaults.
type Config struct {
	DataDir      string        // local state: default database and blobs, TLS files
	Database     string        // "" = SQLite in DataDir; or sqlite:///path, postgres://...
	Blobs        string        // "" = DataDir/objects; or file:///path, s3://bucket/prefix?endpoint=...
	Registration string        // invite (default) | open | closed
	ExpireAfter  time.Duration // default 30 days without access
	Grace        time.Duration // default 7 days after expiry
	TombstoneTTL time.Duration // default 365 days
	GCInterval   time.Duration // default 1 hour
	TokenTTL     time.Duration // default 30 days
	Now          func() time.Time
	Logger       *log.Logger
	Dist         *Distribution // optional: serve /install.sh and the client binary
	AuthPerMin   int           // auth requests per client IP per minute (default 10)
	// DefaultQuota bounds the bytes each account stores (sessions, library,
	// secrets); 0 = unlimited. Per-user quotas override it.
	DefaultQuota int64
	// SiteDir holds organisation defaults served to logged-in clients
	// (GET /v1/site); "" = <DataDir>/site.
	SiteDir string
	// SiteAuth requires a login for GET /v1/site. Off by default: the
	// installer fetches the defaults before the user has an account.
	SiteAuth bool
	// IdentityBrokerURL/Key configure passwordless login through a
	// platform identity broker. The broker answers GET
	// <url>/v3/user/userAuth?userName=<erp> with {"data":{"sk":...}} when
	// the request is signed with Key. Empty = identity login disabled.
	IdentityBrokerURL string
	IdentityBrokerKey string
	// VersionCheck looks up the latest Codex CLI, Claude Code and
	// codex-mixin releases (npm registry, GitHub) every few hours, so the
	// console shows how far behind each machine is.
	VersionCheck bool
}

// Server is an http.Handler backed by a metadata database (SQLite or
// PostgreSQL) and a blob store (local directory or S3). With PostgreSQL and
// S3 any number of instances can serve the same data behind a load balancer.
type Server struct {
	cfg     Config
	site    siteCache
	st      *store
	mux     *http.ServeMux
	secret  []byte // for deterministic fake prelogin salts
	limiter *limiter
	dist    *distState
	auth    *authCache
	search  *searchCache
	quota   *quotaCache
	// settings: runtime settings from the admin panel (settings.go).
	settings settingsCache
	activity *activity
	versions *toolVersions
	adminKey []byte // signs admin panel sessions
	started  time.Time
	// quotaFlag caches whether any account has its own quota.
	quotaFlag struct {
		sync.Mutex
		any bool
		at  time.Time
	}
}

// New opens (or creates) the data directory and returns a ready server.
func New(cfg Config) (*Server, error) {
	if cfg.SiteDir == "" {
		cfg.SiteDir = filepath.Join(cfg.DataDir, "site")
	}
	if cfg.ExpireAfter == 0 {
		cfg.ExpireAfter = 30 * 24 * time.Hour
	}
	if cfg.Grace == 0 {
		cfg.Grace = 7 * 24 * time.Hour
	}
	if cfg.TombstoneTTL == 0 {
		cfg.TombstoneTTL = 365 * 24 * time.Hour
	}
	if cfg.GCInterval == 0 {
		cfg.GCInterval = time.Hour
	}
	if cfg.TokenTTL == 0 {
		cfg.TokenTTL = 30 * 24 * time.Hour
	}
	if cfg.AuthPerMin == 0 {
		cfg.AuthPerMin = 10
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	switch cfg.Registration {
	case "":
		cfg.Registration = RegistrationInvite
	case RegistrationInvite, RegistrationOpen, RegistrationApproval, RegistrationClosed:
	default:
		return nil, fmt.Errorf("unknown registration mode %q (want invite, open, approval or closed)", cfg.Registration)
	}
	st, err := openStore(cfg.DataDir, cfg.Database, cfg.Blobs)
	if err != nil {
		return nil, err
	}
	secret, err := loadOrCreateSecret(st, cfg.DataDir)
	if err != nil {
		st.close()
		return nil, err
	}
	dist, err := newDist(cfg.Dist)
	if err != nil {
		st.close()
		return nil, err
	}
	s := &Server{cfg: cfg, st: st, secret: secret, limiter: newLimiter(cfg.AuthPerMin, time.Minute), dist: dist, auth: newAuthCache(), search: newSearchCache(), quota: newQuotaCache(),
		activity: newActivity(), versions: newToolVersions(), started: time.Now()}
	if s.adminKey, err = st.setting("admin_session_key", func() []byte { return crypto.RandomBytes(32) }); err != nil {
		st.close()
		return nil, err
	}
	s.routes()
	go s.activityLoop()
	if cfg.VersionCheck {
		go s.versionLoop()
	}
	return s, nil
}

// Close releases the database.
func (s *Server) Close() error {
	close(s.activity.stop)
	close(s.versions.stop)
	<-s.activity.done // last counts written
	return s.st.close()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// RunGC performs one expiry pass.
func (s *Server) RunGC() (gcStats, error) {
	now := s.cfg.Now().Unix()
	return s.st.runGC(now, int64(s.cfg.ExpireAfter.Seconds()), int64(s.cfg.Grace.Seconds()), int64(s.cfg.TombstoneTTL.Seconds()))
}

// GCLoop runs RunGC every GCInterval until ctx is cancelled.
func (s *Server) GCLoop(ctx context.Context) {
	t := time.NewTicker(s.cfg.GCInterval)
	defer t.Stop()
	for {
		if st, err := s.RunGC(); err != nil {
			s.cfg.Logger.Printf("gc: %v", err)
		} else if st.Expired+st.Deleted+st.TombstonesPurged > 0 {
			s.cfg.Logger.Printf("gc: expired=%d deleted=%d tombstones_purged=%d", st.Expired, st.Deleted, st.TombstonesPurged)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) now() int64 { return s.cfg.Now().Unix() }

// CreateInvite issues a single-use registration code valid for ttl. It is
// called by the `hearth server invite` command on the server host.
func (s *Server) CreateInvite(ttl time.Duration) (string, error) {
	code := "inv-" + hex.EncodeToString(crypto.RandomBytes(16))
	now := s.now()
	return code, s.st.createInvite(tokenHash(code), now, now+int64(ttl.Seconds()))
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, api.Error{Error: msg})
}

// writeStoreErr maps store errors to HTTP status codes.
func (s *Server) writeStoreErr(w http.ResponseWriter, err error) {
	var ce *conflictError
	switch {
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, api.Error{Error: "conflict", Generation: ce.Generation, Size: ce.Size})
	case errors.Is(err, errNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, errGone):
		writeErr(w, http.StatusGone, "session deleted")
	case errors.Is(err, errLocked):
		writeErr(w, http.StatusLocked, "session leased by another device")
	case errors.Is(err, errExists):
		writeErr(w, http.StatusConflict, "already exists")
	case errors.Is(err, errInvite):
		writeErr(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errForbidden):
		writeErr(w, http.StatusForbidden, "insufficient role in team")
	case errors.Is(err, errBadRequest):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errQuota):
		writeErr(w, http.StatusInsufficientStorage, err.Error()+" (ask the server admin, or delete sessions: hearth session list)")
	default:
		s.cfg.Logger.Printf("internal error: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func tokenHash(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

// authHash hashes the client's auth key. The auth key is already the output
// of Argon2id + HKDF, so a salted SHA-256 is sufficient here.
func authHash(salt, authKey []byte) []byte {
	m := hmac.New(sha256.New, salt)
	m.Write(authKey)
	return m.Sum(nil)
}

func validSessionID(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func fakeSalt(secret []byte, email string) []byte {
	b, _ := hex.DecodeString(crypto.MAC(secret, "prelogin:"+email))
	return b[:16]
}

// limiter is a fixed-window per-key rate limiter for auth endpoints.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-l.window)
	h := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			h = append(h, t)
		}
	}
	if len(h) >= l.max {
		l.hits[key] = h
		return false
	}
	l.hits[key] = append(h, now)
	return true
}
