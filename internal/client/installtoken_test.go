package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// One-time install commands: the console (simulated here as the browser
// does it) wraps the vault key for a random secret; a new machine logs in
// with the token once, without the password, and gets the same vault key.
func TestInstallToken(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	want, err := LoadVault(a.Home)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{}
	post := func(path string, body any, cookie *http.Cookie) (int, []byte, *http.Response) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", en.url+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
			req.Header.Set("X-Hearth-Admin", "1")
		}
		r, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		out, _ := io.ReadAll(r.Body)
		return r.StatusCode, out, r
	}

	// Sign in to the console with the derived login key; the answer carries
	// the password-wrapped vault key, which the browser unwraps.
	pre, err := NewAPI(en.url, "", hc).Prelogin("dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	uk, err := crypto.DeriveUserKeys([]byte(pw), pre.Salt, pre.KDF)
	if err != nil {
		t.Fatal(err)
	}
	code, body, resp := post("/v1/web/login", map[string]any{"email": "dev@example.com", "auth_key": uk.AuthKey}, nil)
	if code != 200 {
		t.Fatalf("web login %d %s", code, body)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "hearth_session" {
			cookie = c
		}
	}
	var lr struct {
		EncVaultKey []byte `json:"enc_vault_key"`
		WrapKey     []byte `json:"wrap_key"`
	}
	if err := json.Unmarshal(body, &lr); err != nil || cookie == nil {
		t.Fatalf("web login answer %s (%v)", body, err)
	}
	vk, err := crypto.UnwrapWithKEK(lr.EncVaultKey, uk.KEK)
	if err != nil || !bytes.Equal(vk, want.Key()) {
		t.Fatalf("console vault key: %v", err)
	}
	// The console keeps the vault key sealed with the session's wrap key: the
	// same key comes back for this session, another session gets another.
	get := func(path string, cookie *http.Cookie) (int, []byte) {
		req, _ := http.NewRequest("GET", en.url+path, nil)
		req.AddCookie(cookie)
		r, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, b
	}
	var wk struct {
		WrapKey []byte `json:"wrap_key"`
	}
	if code, b := get("/v1/me/vault-wrap", cookie); code != 200 || json.Unmarshal(b, &wk) != nil || len(lr.WrapKey) != 32 || !bytes.Equal(wk.WrapKey, lr.WrapKey) {
		t.Fatalf("wrap key: %d %s", code, b)
	}
	_, _, resp2 := post("/v1/web/login", map[string]any{"email": "dev@example.com", "auth_key": uk.AuthKey}, nil)
	for _, c := range resp2.Cookies() {
		if c.Name == "hearth_session" {
			if _, b := get("/v1/me/vault-wrap", c); json.Unmarshal(b, &wk) != nil || bytes.Equal(wk.WrapKey, lr.WrapKey) {
				t.Fatalf("another session got the same wrap key: %s", b)
			}
		}
	}
	if code, _ := get("/v1/me/vault-wrap", &http.Cookie{Name: "hearth_session", Value: "forged"}); code != 401 {
		t.Fatalf("wrap key without a session: %d", code)
	}
	// A session without the sealed copy (opened with a link) asks for the password.
	if code, _, _ := post("/v1/me/vault-key", map[string]any{"auth_key": bytes.Repeat([]byte{1}, 32)}, cookie); code != 403 {
		t.Fatalf("vault key with a wrong password: %d", code)
	}
	if code, b, _ := post("/v1/me/vault-key", map[string]any{"auth_key": uk.AuthKey}, cookie); code != 200 || !bytes.Contains(b, []byte(base64.StdEncoding.EncodeToString(lr.EncVaultKey))) {
		t.Fatalf("vault key: %d %s", code, b)
	}

	makeToken := func() string {
		secret := crypto.RandomBytes(32)
		ah := sha256.Sum256(crypto.InstallAuth(secret))
		wrapped, err := crypto.WrapWithInstall(vk, crypto.InstallKey(secret))
		if err != nil {
			t.Fatal(err)
		}
		code, b, _ := post("/v1/me/install-tokens", map[string]any{"auth_hash": ah[:], "enc_vault_key": wrapped}, cookie)
		var r struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		}
		if code != 201 || json.Unmarshal(b, &r) != nil || r.Email != "dev@example.com" {
			t.Fatalf("install token %d %s", code, b)
		}
		return r.ID + "." + base64.RawURLEncoding.EncodeToString(secret)
	}
	if code, _, _ := post("/v1/me/install-tokens", map[string]any{"auth_hash": []byte("x"), "enc_vault_key": []byte("y")}, cookie); code != 400 {
		t.Fatalf("bad key material: %d", code)
	}

	tok := makeToken()
	if _, err := LoginWithInstallToken(t.TempDir(), en.url, "bob@example.com", tok, "b"); !errors.Is(err, ErrInstallToken) {
		t.Fatalf("another account's email: %v", err)
	}
	tok = makeToken()
	home := t.TempDir()
	cfg, err := LoginWithInstallToken(home, en.url, "dev@example.com", tok, "new-box")
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadVault(home)
	if err != nil || !bytes.Equal(got.Key(), want.Key()) || cfg.DeviceName != "new-box" {
		t.Fatalf("vault after install login: %v", err)
	}
	if _, err := NewAPI(en.url, cfg.Token, hc).Devices(); err != nil {
		t.Fatalf("device token: %v", err)
	}
	if _, err := LoginWithInstallToken(t.TempDir(), en.url, "dev@example.com", tok, "again"); !errors.Is(err, ErrInstallToken) {
		t.Fatalf("second use: %v", err)
	}
	// A wrong secret for a real id does not work and does not burn it.
	tok = makeToken()
	id, _, _ := bytes.Cut([]byte(tok), []byte("."))
	forged := string(id) + "." + base64.RawURLEncoding.EncodeToString(crypto.RandomBytes(32))
	if _, err := LoginWithInstallToken(t.TempDir(), en.url, "dev@example.com", forged, "x"); !errors.Is(err, ErrInstallToken) {
		t.Fatalf("forged secret: %v", err)
	}
	if _, err := LoginWithInstallToken(t.TempDir(), en.url, "dev@example.com", tok, "x"); err != nil {
		t.Fatalf("token after a forged attempt: %v", err)
	}
	if _, err := LoginWithInstallToken(t.TempDir(), en.url, "dev@example.com", "nonsense", "x"); err == nil {
		t.Fatal("accepted a malformed token")
	}
	// Expired after 30 minutes.
	tok = makeToken()
	en.clock.add(31 * time.Minute)
	if _, err := LoginWithInstallToken(t.TempDir(), en.url, "dev@example.com", tok, "late"); !errors.Is(err, ErrInstallToken) {
		t.Fatalf("expired token: %v", err)
	}
	// At most 10 unused commands per user.
	for i := 0; i < 10; i++ {
		makeToken()
	}
	secret := crypto.RandomBytes(32)
	wrapped, _ := crypto.WrapWithInstall(vk, crypto.InstallKey(secret))
	ah := sha256.Sum256(crypto.InstallAuth(secret))
	if code, _, _ := post("/v1/me/install-tokens", map[string]any{"auth_hash": ah[:], "enc_vault_key": wrapped}, cookie); code != 429 {
		t.Fatalf("11th unused command: %d", code)
	}
	if _, err := en.srv.RunGC(); err != nil {
		t.Fatal(err)
	}
}
