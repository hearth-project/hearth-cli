package client

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/server"
)

func TestSelfUpdate(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	os.MkdirAll(dist, 0o755)
	os.WriteFile(filepath.Join(dist, PlatformBinary()), []byte("new build"), 0o755)
	own := filepath.Join(dir, "server-binary")
	os.WriteFile(own, []byte("server"), 0o755)
	var h http.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	defer ts.Close()
	srv, err := server.New(server.Config{DataDir: filepath.Join(dir, "data"), Logger: log.New(io.Discard, "", 0),
		Dist: &server.Distribution{PublicURL: ts.URL, BinaryPath: own, Dir: dist}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	h = srv

	exe := filepath.Join(t.TempDir(), "hearth")
	os.WriteFile(exe, []byte("old build"), 0o755)
	r, err := SelfUpdate(t.TempDir(), ts.URL, exe)
	if err != nil || !r.Updated {
		t.Fatalf("%+v %v", r, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new build" {
		t.Fatalf("binary %q", b)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if r, err := SelfUpdate(t.TempDir(), ts.URL, exe); err != nil || r.Updated {
		t.Fatalf("second run %+v %v", r, err)
	}
	// Replaced on disk after the server computed the manifest: rejected.
	os.WriteFile(filepath.Join(dist, PlatformBinary()), []byte("tampered"), 0o755)
	os.WriteFile(exe, []byte("old build"), 0o755)
	if _, err := SelfUpdate(t.TempDir(), ts.URL, exe); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old build" {
		t.Fatalf("binary replaced despite mismatch: %q", b)
	}
	if entries, _ := os.ReadDir(filepath.Dir(exe)); len(entries) != 1 {
		t.Fatalf("temp files left: %v", entries)
	}
}

// The daemon's automatic update honours the server's switch and the
// machine's opt-out; the manifest carries the build version.
func TestAutoUpdateSwitches(t *testing.T) {
	t.Setenv("HEARTH_AUTO_UPDATE", "")
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	os.MkdirAll(dist, 0o755)
	os.WriteFile(filepath.Join(dist, PlatformBinary()), []byte("new build"), 0o755)
	os.WriteFile(filepath.Join(dist, "VERSION"), []byte("0.2.0\n"), 0o644)
	own := filepath.Join(dir, "server-binary")
	os.WriteFile(own, []byte("server"), 0o755)
	start := func(noAuto bool) string {
		var h http.Handler
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
		t.Cleanup(ts.Close)
		srv, err := server.New(server.Config{DataDir: t.TempDir(), Logger: log.New(io.Discard, "", 0),
			Dist: &server.Distribution{PublicURL: ts.URL, BinaryPath: own, Dir: dist, NoAutoUpdate: noAuto}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { srv.Close() })
		h = srv
		return ts.URL
	}
	exe := filepath.Join(t.TempDir(), "hearth")
	os.WriteFile(exe, []byte("old build"), 0o755)
	home := t.TempDir()

	off := start(true)
	if r, err := SelfUpdateWith(home, off, exe, UpdateOptions{Auto: true}); err != nil || r.Updated || !r.Disabled {
		t.Fatalf("server switch ignored: %+v %v", r, err)
	}
	on := start(false)
	SetAutoUpdate(home, false)
	if r, _ := SelfUpdateWith(home, on, exe, UpdateOptions{Auto: true}); r.Updated || !r.Disabled {
		t.Fatalf("machine opt-out ignored: %+v", r)
	}
	SetAutoUpdate(home, true)
	r, err := SelfUpdateWith(home, on, exe, UpdateOptions{Auto: true})
	if err != nil || !r.Updated || r.Version != "0.2.0" {
		t.Fatalf("auto update: %+v %v", r, err)
	}
	// A manual self-update ignores both switches.
	os.WriteFile(exe, []byte("old build"), 0o755)
	if r, err := SelfUpdate(home, off, exe); err != nil || !r.Updated {
		t.Fatalf("manual: %+v %v", r, err)
	}
}
