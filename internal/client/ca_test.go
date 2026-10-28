package client

import (
	"crypto/tls"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/crypto"
	"github.com/hearth-project/hearth-cli/internal/server"
)

// The installer stores a self-signed server's CA in ~/.hearth before the
// user creates a workspace; `hearth init` and login in the workspace must
// still trust it.
func TestWorkspaceInheritsInstallerCA(t *testing.T) {
	KDF = crypto.KDFParams{Time: 1, MemoryKiB: 19 * 1024, Threads: 1}
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := server.GenerateSelfSigned([]string{"127.0.0.1"}, crt, key, time.Hour); err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Config{DataDir: filepath.Join(dir, "data"), Registration: server.RegistrationOpen, Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewUnstartedServer(srv)
	cert, _ := tls.LoadX509KeyPair(crt, key)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	defer ts.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HEARTH_CA_CERT", "")
	pemData, _ := os.ReadFile(crt)
	os.MkdirAll(DefaultHome(), 0o700)
	os.WriteFile(CAFile(DefaultHome()), pemData, 0o600) // what install.sh does

	if _, err := Register(t.TempDir(), ts.URL, "x@example.com", []byte(pw), ""); err != nil {
		t.Fatalf("register from a fresh home: %v", err)
	}
	// Workspace created after the install: CA copied in, login works.
	ws := filepath.Join(home, "work")
	os.MkdirAll(ws, 0o755)
	wsHome, err := InitWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(CAFile(wsHome)); string(b) != string(pemData) {
		t.Fatal("hearth init did not copy the CA")
	}
	if _, err := Login(wsHome, ts.URL, "x@example.com", []byte(pw), "d"); err != nil {
		t.Fatalf("login in the workspace: %v", err)
	}
	// A workspace made before the fix (no ca.pem) still works and adopts it.
	old := filepath.Join(t.TempDir(), ".hearth")
	os.MkdirAll(old, 0o700)
	if _, err := Login(old, ts.URL, "x@example.com", []byte(pw), "d2"); err != nil {
		t.Fatalf("login without its own CA: %v", err)
	}
	if _, err := os.Stat(CAFile(old)); err != nil {
		t.Fatal("login did not adopt the CA")
	}
	// No CA anywhere: the TLS error stays explicit.
	os.Remove(CAFile(DefaultHome()))
	if _, err := Login(t.TempDir(), ts.URL, "x@example.com", []byte(pw), "d3"); err == nil || !strings.Contains(err.Error(), "x509") {
		t.Fatalf("untrusted server: %v", err)
	}
}
