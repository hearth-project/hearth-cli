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

func TestAutoUpdateSkip(t *testing.T) {
	for _, c := range []struct {
		cur, served string
		skip        bool
	}{
		{"0.1.0-20261030.1156", "0.1.0-20261030.1600", false}, // newer release
		{"0.1.0-20261029.2359", "0.1.0-20261030.0001", false},
		{"0.1.0", "0.1.0-20261030.1156", false},
		{"0.1.0-20261030.1156", "0.2.0", false},
		{"0.1.0-20261030.1156", "0.1.0-20261030.1156", true}, // same version, other build: a branch build
		{"0.1.0-20261030.1600", "0.1.0-20261030.1156", true}, // the server's is older
		{"0.1.0-20261030.1156+isolate", "0.1.0-20261030.1600", true},
		{"0.1.0-dev", "0.1.0-20261030.1600", true}, // plain go build
		{"ca4cac7", "0.1.0-20261030.1600", true},
		{"0.1.0-dev", "0.1.0-dev", false}, // server without release versions: checksums decide
		{"", "0.1.0-20261030.1600", false},
	} {
		if got := AutoUpdateSkip(c.cur, c.served); (got != "") != c.skip {
			t.Errorf("AutoUpdateSkip(%q, %q) = %q, want skip=%v", c.cur, c.served, got, c.skip)
		}
	}
}

// The daemon keeps a branch build that has the release's version (the
// release would rewrite config it does not know); hearth self-update still
// replaces it.
func TestAutoUpdateKeepsBranchBuild(t *testing.T) {
	t.Setenv("HEARTH_AUTO_UPDATE", "")
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	os.MkdirAll(dist, 0o755)
	os.WriteFile(filepath.Join(dist, PlatformBinary()), []byte("release build"), 0o755)
	os.WriteFile(filepath.Join(dist, "VERSION"), []byte("0.1.0-20261030.1156\n"), 0o644)
	own := filepath.Join(dir, "server-binary")
	os.WriteFile(own, []byte("server"), 0o755)
	var h http.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	defer ts.Close()
	srv, err := server.New(server.Config{DataDir: t.TempDir(), Logger: log.New(io.Discard, "", 0),
		Dist: &server.Distribution{PublicURL: ts.URL, BinaryPath: own, Dir: dist}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	h = srv
	exe := filepath.Join(t.TempDir(), "hearth")
	os.WriteFile(exe, []byte("branch build"), 0o755)
	home := t.TempDir()
	for _, cur := range []string{"0.1.0-20261030.1156", "0.1.0-20261030.1156+isolate", "0.1.0-dev"} {
		r, err := SelfUpdateWith(home, ts.URL, exe, UpdateOptions{Auto: true, Current: cur})
		if err != nil || r.Updated || r.Skipped == "" {
			t.Fatalf("%s replaced: %+v %v", cur, r, err)
		}
	}
	if r, err := SelfUpdateWith(home, ts.URL, exe, UpdateOptions{Auto: true, Current: "0.1.0-20261029.0900"}); err != nil || !r.Updated {
		t.Fatalf("older release kept: %+v %v", r, err)
	}
	os.WriteFile(exe, []byte("branch build"), 0o755)
	if r, err := SelfUpdate(home, ts.URL, exe); err != nil || !r.Updated {
		t.Fatalf("manual: %+v %v", r, err)
	}
}
