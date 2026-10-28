package client

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/crypto"
	"github.com/hearth-project/hearth-cli/internal/server"
	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

// The identity broker is the platform's delegated-trust service: it maps a
// user name to that user's platform secret.
func TestPasswordlessIdentityLogin(t *testing.T) {
	isolateAgentHomes(t)
	KDF = crypto.KDFParams{Time: 1, MemoryKiB: 19 * 1024, Threads: 1}
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("userName") != "dev" || r.Header.Get("Authorization") == "" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"sk": "platform-secret"}})
	}))
	defer broker.Close()

	data := t.TempDir()
	dbURL, blobs := servertest.Backends(t)
	srv, err := server.New(server.Config{DataDir: data, Database: dbURL, Blobs: blobs,
		Now: time.Now, TokenTTL: 24 * time.Hour,
		AuthPerMin: 1000, Logger: log.New(io.Discard, "", 0),
		IdentityBrokerURL: broker.URL, IdentityBrokerKey: "system-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	hs := httptest.NewServer(srv)
	defer hs.Close()
	invite, err := srv.CreateInvite(time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	if _, err := Register(home, hs.URL, "dev@example.com", []byte(pw), invite); err != nil {
		t.Fatal(err)
	}
	if _, err := Login(home, hs.URL, "dev@example.com", []byte(pw), "first"); err != nil {
		t.Fatal(err)
	}
	// Startup enrollment is automatic: the machine has the platform secret.
	t.Setenv("SYS_USER_NAME", "dev")
	t.Setenv("PDC_USER_SK", "platform-secret")
	if err := AutoEnableIdentityLogin(home); err != nil {
		t.Fatal(err)
	}

	// A new machine with the platform secret logs in without a password.
	fresh := t.TempDir()
	cfg, err := LoginWithIdentity(fresh, hs.URL, "dev@example.com", []byte("platform-secret"), "second")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Email != "dev@example.com" || cfg.Token == "" {
		t.Fatalf("config %+v", cfg)
	}
	v, err := LoadVault(fresh)
	if err != nil || len(v.Key()) != crypto.KeySize {
		t.Fatalf("vault not restored: %v", err)
	}

	// The wrong platform secret must not log in.
	other := t.TempDir()
	if _, err := LoginWithIdentity(other, hs.URL, "dev@example.com", []byte("wrong"), "third"); StatusOf(err) != 401 {
		t.Fatalf("wrong secret: %v", err)
	}

	// An explicit disable is sticky: startup must not silently re-enable it.
	if err := DisableIdentityLogin(home); err != nil {
		t.Fatal(err)
	}
	if err := AutoEnableIdentityLogin(home); err != nil {
		t.Fatal(err)
	}
	after := t.TempDir()
	if _, err := LoginWithIdentity(after, hs.URL, "dev@example.com", []byte("platform-secret"), "fourth"); StatusOf(err) != 401 {
		t.Fatalf("identity login re-enabled after disable: %v", err)
	}
}
