package server

import (
	"archive/tar"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// Run the actual POSIX one-liner with a wget that only supports plain HTTP.
// The test bundle's executable records setup arguments without contacting an API.
func TestOfflineBundlePairing(t *testing.T) {
	realWget, err := exec.LookPath("wget")
	if err != nil {
		t.Skip("requires wget")
	}
	var s Server
	name := "hearth-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar"
	var requests atomic.Int32
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != "/bootstrap/"+name {
			t.Errorf("unexpected bundle request %q", r.URL.String())
		}
		requests.Add(1)
		s.BootstrapHandler().ServeHTTP(w, r)
	}))
	defer mirror.Close()
	bin := filepath.Join(t.TempDir(), "hearth")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nif [ \"$1\" = setup ]; then printf '%s\\n' \"$@\" > \"$HOME/args\"; fi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	d := &Distribution{PublicURL: "https://127.0.0.1:1", BootstrapURL: mirror.URL, BinaryPath: bin}
	s.dist, err = newDist(d)
	if err != nil {
		t.Fatal(err)
	}
	cmdline := s.installForConsole() + " --email 'me@example.org' --token DEMO"
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
	shim := t.TempDir()
	if err := os.WriteFile(filepath.Join(shim, "wget"), []byte("#!/bin/sh\nfor a do case \"$a\" in https:*) exit 4;; esac; done\nexec "+shellArg(realWget)+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shim, "curl"), []byte("#!/bin/sh\nexit 35\n"), 0755); err != nil {
		t.Fatal(err)
	}
	run := func() (string, string, error) {
		userDir := t.TempDir()
		cmd := exec.Command("sh", "-c", cmdline)
		cmd.Env = []string{"PATH=" + shim + ":" + os.Getenv("PATH"), "HOME=" + userDir, "HEARTH_NO_PATH=1", "HEARTH_NO_SKILLS=1"}
		out, err := cmd.CombinedOutput()
		return userDir, string(out), err
	}
	userDir, out, err := run()
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	args, _ := os.ReadFile(filepath.Join(userDir, "args"))
	if string(args) != "setup\n--server\n"+d.PublicURL+"\n--email\nme@example.org\n--token\nDEMO\n" {
		t.Fatalf("setup args %q", args)
	}
	if requests.Load() != 1 {
		t.Fatalf("download count %d", requests.Load())
	}
	if err := os.WriteFile(bin, []byte("different build"), 0755); err != nil {
		t.Fatal(err)
	}
	userDir, out, err = run()
	if err == nil || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("changed tar: %v %s", err, out)
	}
	if _, err = os.Stat(filepath.Join(userDir, ".local/bin/hearth")); !os.IsNotExist(err) {
		t.Fatal("unverified tar installed")
	}
	if err := os.WriteFile(filepath.Join(shim, "uname"), []byte("#!/bin/sh\ncase $1 in -s) echo Linux;; -m) echo x86_64;; -r) echo 2.6.32;; esac\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		_, out, err = run()
		if err == nil || !strings.Contains(out, "3.2 or newer") || requests.Load() != 2 {
			t.Fatalf("unsupported kernel: %v %s %v", err, out, requests.Load())
		}
	}
}
