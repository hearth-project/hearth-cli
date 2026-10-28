package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Admin panel authentication. An admin is a user listed in the admins
// table (`hearth server admin grant <email>` on the server). Admins sign in
// with their account: the page derives the login key from the master
// password in the browser (Argon2id, like the client), or they open a
// short-lived link from `hearth panel` / `hearth server admin link`. Both
// set a signed, HttpOnly, SameSite=Strict session cookie. Mutating requests also need the X-Hearth-Admin header,
// which other sites cannot send.

const (
	adminCookie     = "hearth_session"
	adminSessionTTL = 12 * time.Hour
	adminLinkTTL    = 10 * time.Minute
	adminHeader     = "X-Hearth-Admin"
)

type adminClaims struct {
	UID     int64  `json:"u"`
	Exp     int64  `json:"e"`
	Purpose string `json:"p"` // link | session
}

func (s *Server) signAdmin(c adminClaims) string {
	b, _ := json.Marshal(c)
	p := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, s.adminKey)
	m.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) verifyAdmin(tok, purpose string) (int64, error) {
	p, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return 0, errors.New("malformed")
	}
	m := hmac.New(sha256.New, s.adminKey)
	m.Write([]byte(p))
	want := m.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want) {
		return 0, errors.New("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return 0, err
	}
	var c adminClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return 0, err
	}
	if c.Purpose != purpose || c.Exp < s.now() {
		return 0, errors.New("expired")
	}
	return c.UID, nil
}

// isAdmin reports whether uid is an active admin.
func (s *Server) isAdmin(uid int64) bool {
	var n int
	err := s.st.rdb.QueryRow(`SELECT COUNT(*) FROM admins a JOIN users u ON u.id=a.user_id WHERE a.user_id=? AND u.status='active'`, uid).Scan(&n)
	return err == nil && n > 0
}

// AdminLink returns a panel login link (valid adminLinkTTL) for an admin.
func (s *Server) AdminLink(baseURL, email string) (string, error) {
	uid, err := s.uidOf(email)
	if err != nil {
		return "", err
	}
	t := s.signAdmin(adminClaims{UID: uid, Exp: s.now() + int64(adminLinkTTL.Seconds()), Purpose: "link"})
	return strings.TrimRight(baseURL, "/") + "/app/login?t=" + t, nil
}

// SetAdmin adds or removes an admin; the last admin cannot be removed.
func (s *Server) SetAdmin(email string, on bool) error {
	uid, err := s.uidOf(email)
	if err != nil {
		return err
	}
	if on {
		_, err = s.st.db.Exec(`INSERT INTO admins(user_id,added_at) VALUES(?,?) ON CONFLICT(user_id) DO NOTHING`, uid, s.now())
		return err
	}
	var n int
	if err := s.st.db.QueryRow(`SELECT COUNT(*) FROM admins WHERE user_id<>?`, uid).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s is the last admin; add another one first", errBadRequest, email)
	}
	_, err = s.st.db.Exec(`DELETE FROM admins WHERE user_id=?`, uid)
	return err
}

// Admins lists admin emails.
func (s *Server) Admins() ([]string, error) {
	rows, err := s.st.rdb.Query(`SELECT u.email FROM admins a JOIN users u ON u.id=a.user_id ORDER BY u.email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Audit records an admin action (best effort).
func (s *Server) Audit(actor, action, target, detail string) {
	if _, err := s.st.db.Exec(`INSERT INTO admin_audit(at,actor,action,target,detail) VALUES(?,?,?,?,?)`, s.now(), actor, action, target, detail); err != nil {
		s.cfg.Logger.Printf("audit: %v", err)
	}
}

type adminCtx struct {
	uid   int64
	email string
}

// admin wraps a panel API handler: valid session cookie of an active
// admin, and the CSRF header on anything but GET.
func (s *Server) admin(h func(http.ResponseWriter, *http.Request, adminCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(adminCookie)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "not signed in to the admin panel (run: hearth panel)")
			return
		}
		uid, err := s.verifyAdmin(c.Value, "session")
		if err != nil || !s.isAdmin(uid) {
			writeErr(w, http.StatusUnauthorized, "admin session expired or revoked (run: hearth panel)")
			return
		}
		if r.Method != http.MethodGet && r.Header.Get(adminHeader) != "1" {
			writeErr(w, http.StatusForbidden, "missing "+adminHeader+" header")
			return
		}
		email, _ := s.st.emailOf(uid)
		w.Header().Set("Cache-Control", "no-store")
		h(w, r, adminCtx{uid: uid, email: email})
	}
}

// handleAdminLogin exchanges a link token for a session cookie.
func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	uid, err := s.verifyAdmin(r.URL.Query().Get("t"), "link")
	if err != nil || !s.activeUser(uid) {
		http.Error(w, "This sign-in link is invalid or expired. Get a new one with: hearth panel", http.StatusUnauthorized)
		return
	}
	s.setAdminCookie(w, r, uid)
	if email, err := s.st.emailOf(uid); err == nil && s.isAdmin(uid) {
		s.Audit(email, "panel-login", "", "link "+clientIP(r))
	}
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

// handleAdminPasswordLogin signs an admin in with email and the login key
// the panel derived in the browser from the master password (the same key
// the client sends to /v1/auth/login; the password itself never arrives).
func (s *Server) handleAdminPasswordLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		AuthKey  []byte `json:"auth_key"`
		Remember bool   `json:"remember"` // 7 days instead of 12 hours
	}
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
	ttl := adminSessionTTL
	if req.Remember {
		ttl = rememberTTL
	}
	s.setSessionCookie(w, r, u.ID, ttl)
	if s.isAdmin(u.ID) {
		s.Audit(email, "panel-login", "", "password "+clientIP(r))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setAdminCookie(w http.ResponseWriter, r *http.Request, uid int64) {
	s.setSessionCookie(w, r, uid, adminSessionTTL)
}

// rememberTTL: "remember this computer" on the sign-in page.
const rememberTTL = 7 * 24 * time.Hour

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, uid int64, ttl time.Duration) {
	tok := s.signAdmin(adminClaims{UID: uid, Exp: s.now() + int64(ttl.Seconds()), Purpose: "session"})
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: tok, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminLinkAPI: a logged-in client (bearer token) of an admin asks
// for a panel link (`hearth panel`).
func (s *Server) handleAdminLinkAPI(w http.ResponseWriter, r *http.Request) {
	uid := userOf(r)
	t := s.signAdmin(adminClaims{UID: uid, Exp: s.now() + int64(adminLinkTTL.Seconds()), Purpose: "link"})
	writeJSON(w, http.StatusOK, map[string]string{"path": "/app/login?t=" + t})
}
