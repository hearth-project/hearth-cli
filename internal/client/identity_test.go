package client

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeJWT(claims string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc([]byte(claims)) + ".sig"
}

func identityEnv(t *testing.T, wsName string) (home, ws string) {
	t.Setenv("HEARTH_IDENTITY", "")
	t.Setenv("COMATE_USERNAME", "")
	t.Setenv("SYS_USER_NAME", "")
	homeRoot := t.TempDir()
	home = filepath.Join(homeRoot, wsName, ".hearth")
	ws = filepath.Join(homeRoot, wsName)
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEARTH_WORKSPACE", ws)
	t.Setenv("HOME", t.TempDir())
	return home, ws
}

func writeSiteLearn(t *testing.T, home string) {
	p := filepath.Join(home, "site", "learn.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	body := `{"token_files":["~/.config/sso/.token_*"],"identity":{"email_domain":"example.com"}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSSOToken(t *testing.T, username string) {
	home := os.Getenv("HOME")
	p := filepath.Join(home, ".config", "sso", ".token_"+username)
	os.MkdirAll(filepath.Dir(p), 0o700)
	tok := `{"token":"` + fakeJWT(`{"iss":"sso","username":"`+username+`"}`) + `","permanent":true}`
	if err := os.WriteFile(p, []byte(tok), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDetectIdentityFromSSOToken(t *testing.T) {
	home, _ := identityEnv(t, "alice")
	writeSiteLearn(t, home)
	writeSSOToken(t, "alice")

	id, ok := DetectIdentity(home)
	if !ok || id.Username != "alice" || id.Email != "alice@example.com" {
		t.Fatalf("detected %+v ok=%v", id, ok)
	}
	if id.Source != "token ~/.config/sso/.token_alice" {
		t.Fatalf("source %q", id.Source)
	}
}

func TestDetectIdentityDoesNotGuessBetweenUsers(t *testing.T) {
	home, _ := identityEnv(t, "shared")
	writeSiteLearn(t, home)
	writeSSOToken(t, "alice")
	writeSSOToken(t, "bob")

	if id, ok := DetectIdentity(home); ok {
		t.Fatalf("guessed an account on a shared user: %+v", id)
	}
}

func TestDetectIdentityPrefersExistingLogin(t *testing.T) {
	home, _ := identityEnv(t, "alice")
	writeSiteLearn(t, home)
	writeSSOToken(t, "bob")
	if err := saveJSON(filepath.Join(home, "config.json"), Config{Email: "carol@example.com", Server: "https://x"}); err != nil {
		t.Fatal(err)
	}

	id, ok := DetectIdentity(home)
	if !ok || id.Email != "carol@example.com" || id.Source != "hearth config" {
		t.Fatalf("detected %+v ok=%v", id, ok)
	}
}

func TestDetectIdentityEnvOverride(t *testing.T) {
	home, _ := identityEnv(t, "alice")
	writeSiteLearn(t, home)
	t.Setenv("HEARTH_IDENTITY", "dave@other.example")

	id, ok := DetectIdentity(home)
	if !ok || id.Email != "dave@other.example" || id.Username != "dave" {
		t.Fatalf("detected %+v ok=%v", id, ok)
	}
}

func TestDetectIdentityComateLoginFiles(t *testing.T) {
	home, _ := identityEnv(t, "alice")
	writeSiteLearn(t, home)
	p := filepath.Join(os.Getenv("HOME"), ".comate", "login-user", "alice")
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("Bearer-x"), 0o600)

	id, ok := DetectIdentity(home)
	if !ok || id.Username != "alice" || !strings.HasPrefix(id.Source, "comate login") {
		t.Fatalf("detected %+v ok=%v", id, ok)
	}
}

// Shared containers inject the user who started them. That is not proof
// that the person using this workspace is that user.
func TestDetectIdentityIgnoresStaleEnvHint(t *testing.T) {
	home, _ := identityEnv(t, "alice")
	writeSiteLearn(t, home)
	t.Setenv("SYS_USER_NAME", "bob")

	if id, ok := DetectIdentity(home); ok {
		t.Fatalf("trusted a container-level user name: %+v", id)
	}

	t.Setenv("SYS_USER_NAME", "alice")
	if id, ok := DetectIdentity(home); !ok || id.Username != "alice" {
		t.Fatalf("workspace match ignored: %+v ok=%v", id, ok)
	}
}

// Switching accounts still knows the mail domain from the existing login.
func TestDetectLocalIdentityDomainFromExistingAccount(t *testing.T) {
	home, _ := identityEnv(t, "bob")
	p := filepath.Join(home, "site", "learn.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"token_files":["~/.config/sso/.token_*"]}`), 0o644)
	writeSSOToken(t, "bob")
	if err := saveJSON(filepath.Join(home, "config.json"), Config{Email: "carol@corp.example.com", Server: "https://x"}); err != nil {
		t.Fatal(err)
	}

	id, ok := DetectLocalIdentity(home)
	if !ok || id.Username != "bob" || id.Email != "bob@corp.example.com" {
		t.Fatalf("detected %+v ok=%v", id, ok)
	}
}

// A workspace made after installing has no site defaults of its own yet:
// the organisation's token files and mail domain come from ~/.hearth.
func TestDetectIdentityInNewWorkspaceUsesDefaultHomeSite(t *testing.T) {
	home, _ := identityEnv(t, "alice")
	writeSiteLearn(t, DefaultHome())
	writeSSOToken(t, "alice")

	id, ok := DetectIdentity(home)
	if !ok || id.Username != "alice" || id.Email != "alice@example.com" {
		t.Fatalf("detected %+v ok=%v", id, ok)
	}
}

// Without a configured domain, the mail domain of ~/.hearth's login is
// used (only the domain: that login may be another person's).
func TestDetectIdentityDomainFromDefaultHomeLogin(t *testing.T) {
	home, _ := identityEnv(t, "alice")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "none"))
	p := filepath.Join(os.Getenv("HOME"), ".comate", "login-user", "alice")
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("Bearer-x"), 0o600)
	if err := saveJSON(filepath.Join(DefaultHome(), "config.json"), Config{Email: "bob@corp.example.com", Server: "https://x"}); err != nil {
		t.Fatal(err)
	}

	id, ok := DetectIdentity(home)
	if !ok || id.Email != "alice@corp.example.com" {
		t.Fatalf("detected %+v ok=%v", id, ok)
	}
}

func TestMovableLogin(t *testing.T) {
	_, ws := identityEnv(t, "alice")
	home, err := InitWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := MovableLogin(home); ok {
		t.Fatal("movable without a login in ~/.hearth")
	}
	if err := saveJSON(filepath.Join(DefaultHome(), "config.json"), Config{Email: "alice@example.com", Server: "https://x"}); err != nil {
		t.Fatal(err)
	}
	// Empty directories (hearth mcp makes library/) are no state.
	os.MkdirAll(filepath.Join(home, "library", "skills"), 0o700)
	if cfg, ok := MovableLogin(home); !ok || cfg.Email != "alice@example.com" {
		t.Fatalf("not movable: %+v %v", cfg, ok)
	}
	if _, ok := MovableLogin(DefaultHome()); ok {
		t.Fatal("~/.hearth is movable into itself")
	}
	os.WriteFile(filepath.Join(home, "library", "config.toml"), []byte("x"), 0o600)
	if name := OwnState(home); name != "library" {
		t.Fatalf("own state %q", name)
	}
	if _, ok := MovableLogin(home); ok {
		t.Fatal("movable over existing state")
	}
}
