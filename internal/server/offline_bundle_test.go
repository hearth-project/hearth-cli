package server

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The optional download-only listener must not change pairing or expose an
// executable bootstrap script. Its archive hashes come from the main server.
func TestOfflineBundlesAndPairing(t *testing.T) {
	name := "hearth-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar"
	bin := filepath.Join(t.TempDir(), "hearth")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nif [ \"$1\" = setup ]; then printf '%s\\n' \"$@\" > \"$HOME/args\"; fi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	d := &Distribution{PublicURL: "https://127.0.0.1:1", BootstrapURL: "http://127.0.0.1:8788", BinaryPath: bin}
	s, err := New(Config{DataDir: t.TempDir(), Dist: d})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cmdline := s.installForConsole()
	if want := `setup=$(curl -fsSL ` + d.PublicURL + `/install.sh) && sh -c "$setup" sh`; cmdline != want {
		t.Fatalf("copied command: got %q, want %q", cmdline, want)
	}
	if !strings.Contains(cmdline, d.PublicURL+"/install.sh") || strings.Contains(cmdline, d.BootstrapURL) || strings.Contains(cmdline, ".tar") {
		t.Fatalf("unexpected pairing command: %s", cmdline)
	}
	for _, path := range []string{"/", "/install.sh", "/v1/me", "/v1/auth/install/login", "/v1/dist", "/bootstrap/unknown.tar"} {
		r := httptest.NewRecorder()
		s.BootstrapHandler().ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 404 {
			t.Fatalf("%s is reachable: %d", path, r.Code)
		}
	}
	r := httptest.NewRecorder()
	s.BootstrapHandler().ServeHTTP(r, httptest.NewRequest("POST", "/bootstrap/"+name, nil))
	if r.Code != 405 {
		t.Fatalf("POST: %d", r.Code)
	}
	// A manually downloaded bundle contains exactly the installer and executable.
	r = httptest.NewRecorder()
	s.BootstrapHandler().ServeHTTP(r, httptest.NewRequest("GET", "/bootstrap/"+name, nil))
	if r.Code != 200 {
		t.Fatalf("download: %d", r.Code)
	}
	rec := httptest.NewRecorder()
	s.handleDistManifest(rec, httptest.NewRequest("GET", "/v1/dist", nil))
	var manifest struct{ Bundles map[string]string }
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil || manifest.Bundles[name] != fmt.Sprintf("%x", sha256.Sum256(r.Body.Bytes())) {
		t.Fatalf("offline archive manifest: %v %+v", err, manifest)
	}
	tr := tar.NewReader(bytes.NewReader(r.Body.Bytes()))
	for _, want := range []string{"install.sh", "hearth"} {
		h, err := tr.Next()
		if err != nil || h.Name != want {
			t.Fatalf("member: %v %v", h, err)
		}
	}
	if _, err := tr.Next(); err != io.EOF {
		t.Fatalf("unexpected member: %v", err)
	}
	// An old curl can fail before the installer starts, even after writing some
	// bytes. Do not execute a partial script or silently return success.
	shim := t.TempDir()
	curl := filepath.Join(shim, "curl")
	if err := os.WriteFile(curl, []byte("#!/bin/sh\necho 'touch ran-partial-script'\necho 'curl: (35) TLS handshake failed' >&2\nexit 35\n"), 0755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	run := func() (string, error) {
		c := exec.Command("sh", "-c", cmdline+" --email 'me@example.org' --token TOKEN")
		c.Dir = dir
		c.Env = []string{"PATH=" + shim + ":" + os.Getenv("PATH")}
		b, err := c.CombinedOutput()
		return string(b), err
	}
	out, err := run()
	if code, ok := err.(*exec.ExitError); !ok || code.ExitCode() != 35 || !strings.Contains(out, "TLS handshake failed") {
		t.Fatalf("TLS failure: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran-partial-script")); !os.IsNotExist(err) {
		t.Fatal("ran incomplete download")
	}
	// Once the installer has been fetched it can give a Hearth-specific hint.
	r = httptest.NewRecorder()
	s.handleInstall(r, httptest.NewRequest("GET", "/install.sh", nil))
	homeDir := t.TempDir()
	install := exec.Command("sh", "-s", "--", "--install-only")
	install.Env = []string{"PATH=" + shim + ":" + os.Getenv("PATH"), "HOME=" + homeDir}
	install.Stdin = strings.NewReader(r.Body.String())
	if out, err := install.CombinedOutput(); err == nil || !strings.Contains(string(out), "TLS 1.2 or newer") {
		t.Fatalf("installer download diagnostic: %v %s", err, out)
	}
	if err := os.WriteFile(curl, []byte("#!/bin/sh\ncat <<'SCRIPT'\nprintf '%s\\n' \"$@\"\nSCRIPT\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := run(); err != nil || out != "--email\nme@example.org\n--token\nTOKEN\n" {
		t.Fatalf("pairing arguments: %v %q", err, out)
	}
}
