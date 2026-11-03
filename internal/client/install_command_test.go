package client

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/crypto"
	"github.com/hearth-project/hearth-cli/internal/server"
)

// A logged-in machine makes the console's one-time install command itself;
// its token logs a new machine in once, with the same vault key.
func TestMakeInstallCommand(t *testing.T) {
	KDF = crypto.KDFParams{Time: 1, MemoryKiB: 19 * 1024, Threads: 1}
	dir := t.TempDir()
	own := filepath.Join(dir, "server-binary")
	os.WriteFile(own, []byte("server"), 0o755)
	var h http.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	defer ts.Close()
	srv, err := server.New(server.Config{DataDir: filepath.Join(dir, "data"), Logger: log.New(io.Discard, "", 0), AuthPerMin: 1000,
		Dist: &server.Distribution{PublicURL: ts.URL, BinaryPath: own}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	h = srv
	invite, err := srv.CreateInvite(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Register(t.TempDir(), ts.URL, "dev@example.com", []byte(pw), invite); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if _, err := Login(home, ts.URL, "dev@example.com", []byte(pw), "a"); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	ic, err := e.MakeInstallCommand()
	if err != nil {
		t.Fatal(err)
	}
	prefix := "curl -fsSL " + ts.URL + "/install.sh | sh -s -- --email dev@example.com --token "
	if !ic.OneTime || ic.ExpiresAt == 0 || !strings.HasPrefix(ic.Command, prefix) {
		t.Fatalf("command %+v", ic)
	}
	tok := strings.TrimPrefix(ic.Command, prefix)
	nb := t.TempDir()
	if _, err := LoginWithInstallToken(nb, ts.URL, "dev@example.com", tok, "b"); err != nil {
		t.Fatal(err)
	}
	want, _ := LoadVault(home)
	got, err := LoadVault(nb)
	if err != nil || !bytes.Equal(got.Key(), want.Key()) {
		t.Fatalf("vault key on the new machine: %v", err)
	}
	if _, err := LoginWithInstallToken(t.TempDir(), ts.URL, "dev@example.com", tok, "c"); err == nil {
		t.Fatal("token used twice")
	}
}

func TestWithInstallToken(t *testing.T) {
	for _, c := range []struct{ cmd, email, want string }{
		{"curl -fsSL https://h/install.sh | sh", "a@b.c", "curl -fsSL https://h/install.sh | sh -s -- --email a@b.c --token T"},
		{"{ curl a || curl b; } | sh", "o'k@b.c", `{ curl a || curl b; } | sh -s -- --email 'o'\''k@b.c' --token T`},
	} {
		if got := WithInstallToken(c.cmd, c.email, "T"); got != c.want {
			t.Errorf("got  %s\nwant %s", got, c.want)
		}
	}
}
