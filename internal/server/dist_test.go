package server

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstallerAndDownloadWithSelfSignedCert(t *testing.T) {
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := GenerateSelfSigned([]string{"127.0.0.1", "sync.internal"}, crt, key, time.Hour); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(key); st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", st.Mode().Perm())
	}
	pemData, _ := os.ReadFile(crt)
	bin := filepath.Join(dir, "fake-binary")
	os.WriteFile(bin, []byte("binary-content"), 0o755)
	srv, err := New(Config{DataDir: filepath.Join(dir, "data"), Logger: log.New(io.Discard, "", 0),
		Dist: &Distribution{PublicURL: "https://127.0.0.1:1/", BinaryPath: bin, CertPEM: pemData}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ts := httptest.NewUnstartedServer(srv)
	cert, _ := tls.LoadX509KeyPair(crt, key)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	defer ts.Close()

	// A client trusting only the generated cert can connect (IP SAN works).
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemData)
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	get := func(p string) (int, string) {
		resp, err := hc.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	code, script := get("/install.sh")
	sum := sha256.Sum256([]byte("binary-content"))
	pin, _ := SPKIPin(pemData)
	for _, want := range []string{"URL='https://127.0.0.1:1'", "SHA256='" + hex.EncodeToString(sum[:]) + "'",
		"BEGIN CERTIFICATE", "--cacert", "setup --server"} {
		if code != 200 || !strings.Contains(script, want) {
			t.Fatalf("install.sh missing %q (status %d)", want, code)
		}
	}
	// Default: plain -k (the installer pins the embedded cert afterwards).
	if c := srv.InstallCommand(); c != "curl -fsSL -k "+ts.URL+"/install.sh | sh" && !strings.HasSuffix(c, "/install.sh | sh") || strings.Contains(c, "pinnedpubkey") {
		t.Fatalf("default install command: %s", c)
	}
	srv.dist.PinInstall = true
	if !strings.Contains(srv.InstallCommand(), "--pinnedpubkey '"+pin+"'") {
		t.Fatalf("install command lacks pin: %s", srv.InstallCommand())
	}
	if code, body := get("/download/" + srv.dist.names()[0]); code != 200 || body != "binary-content" {
		t.Fatalf("download: %d %q", code, body)
	}
	if code, _ := get("/download/hearth-evil-os"); code != 404 {
		t.Fatalf("unknown binary: %d", code)
	}
	if code, _ := get("/download/..%2f..%2fetc%2fpasswd"); code != 404 {
		t.Fatalf("traversal: %d", code)
	}
}

func TestInstallerDisabledWithoutPublicURL(t *testing.T) {
	srv, err := New(Config{DataDir: t.TempDir(), Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/install.sh", nil))
	if rec.Code != 404 {
		t.Fatalf("status %d", rec.Code)
	}
	if _, err := New(Config{DataDir: t.TempDir(), Dist: &Distribution{PublicURL: "https://x/'; rm -rf ~", BinaryPath: "/bin/sh"}}); err == nil {
		t.Fatal("unsafe public URL accepted")
	}
}

// The installer picks the build for the host (uname), verifies its checksum
// and runs setup; hosts without a build get a clear error.
func TestInstallerPicksPlatformBuild(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	os.MkdirAll(dist, 0o755)
	fake := func(name string) string {
		p := filepath.Join(dist, name)
		os.WriteFile(p, []byte("#!/bin/sh\necho \"ran "+name+" $*\"\n"), 0o755)
		return p
	}
	own := fake("hearth-" + runtime.GOOS + "-" + runtime.GOARCH)
	fake("hearth-linux-arm64")
	fake("hearth-darwin-arm64")
	os.WriteFile(filepath.Join(dist, "hearth-windows-amd64.exe"), []byte("x"), 0o755) // ignored
	var h http.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	defer ts.Close()
	srv, err := New(Config{DataDir: filepath.Join(dir, "data"), Logger: log.New(io.Discard, "", 0),
		Dist: &Distribution{PublicURL: ts.URL, BinaryPath: own, Dir: dist}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	h = srv

	resp, err := http.Get(ts.URL + "/v1/dist")
	if err != nil {
		t.Fatal(err)
	}
	var m struct{ Binaries map[string]string }
	json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if len(m.Binaries) != 3 || m.Binaries["hearth-darwin-arm64"] == "" {
		t.Fatalf("manifest %+v", m)
	}

	var runArgs func(unameS, unameM, args string) (string, error)
	var lastHome string
	linkDir := t.TempDir() // never the real /usr/local/bin
	run := func(unameS, unameM string) (string, error) { return runArgs(unameS, unameM, "--email me@example.com") }
	runArgs = func(unameS, unameM, args string) (string, error) {
		shim := filepath.Join(t.TempDir(), "bin")
		os.MkdirAll(shim, 0o755)
		os.WriteFile(filepath.Join(shim, "uname"), []byte("#!/bin/sh\ncase \"$1\" in -s) echo "+unameS+";; -m) echo "+unameM+";; esac\n"), 0o755)
		home := t.TempDir()
		cmd := exec.Command("sh", "-c", "curl -fsSL "+ts.URL+"/install.sh | sh -s -- "+args)
		cmd.Env = []string{"PATH=" + shim + ":" + os.Getenv("PATH"), "HOME=" + home, "HEARTH_PASSWORD=x", "HEARTH_LINK_DIRS=" + linkDir}
		out, err := cmd.CombinedOutput()
		lastHome = home
		return string(out), err
	}
	out, err := run("Linux", "aarch64")
	if err != nil || !strings.Contains(out, "ran hearth-linux-arm64 setup --server "+ts.URL+" --email me@example.com") {
		t.Fatalf("linux arm64: %v\n%s", err, out)
	}
	// --install-only: binary installed, setup not run (the agent skill drives it).
	// Skills are installed for the machine's agents either way.
	if out, err := runArgs("Linux", "aarch64", "--install-only"); err != nil || strings.Contains(out, "ran hearth-linux-arm64 setup") ||
		!strings.Contains(out, "ran hearth-linux-arm64 onboard install-skill --quiet") || !strings.Contains(out, "next: hearth setup") {
		t.Fatalf("install-only: %v\n%s", err, out)
	}
	// New terminals find hearth: PATH line in the startup files, added once.
	rc, _ := os.ReadFile(filepath.Join(lastHome, ".bashrc"))
	if !strings.Contains(string(rc), lastHome+"/.local/bin:$PATH") {
		t.Fatalf(".bashrc: %q", rc)
	}
	if out, _ := exec.Command("bash", "-c", "HOME="+lastHome+"; . "+lastHome+"/.bashrc; command -v hearth").Output(); !strings.Contains(string(out), "/.local/bin/hearth") {
		t.Fatalf("new shell does not find hearth: %q", out)
	}
	cmd := exec.Command("sh", "-c", "curl -fsSL "+ts.URL+"/install.sh | sh -s -- --install-only")
	cmd.Env = []string{"PATH=" + linkDir + ":" + os.Getenv("PATH"), "HOME=" + lastHome, "HEARTH_LINK_DIRS=" + linkDir}
	cmd.CombinedOutput()
	rc, _ = os.ReadFile(filepath.Join(lastHome, ".bashrc"))
	if strings.Count(string(rc), "added by the hearth installer") != 1 {
		t.Fatalf("PATH line added twice: %q", rc)
	}
	// A writable directory already on PATH gets a link: works without a new shell.
	if dst, err := os.Readlink(filepath.Join(linkDir, "hearth")); err != nil || dst != lastHome+"/.local/bin/hearth" {
		t.Fatalf("link: %q %v", dst, err)
	}
	resp, err = http.Get(ts.URL + "/onboard/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	skill, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(skill), "name: hearth\n") || !strings.Contains(string(skill), "This server: "+ts.URL) {
		t.Fatalf("onboard skill:\n%.400s", skill)
	}
	if out, err := run("Darwin", "arm64"); err != nil || !strings.Contains(out, "ran hearth-darwin-arm64") {
		t.Fatalf("darwin arm64: %v\n%s", err, out)
	}
	if out, err := run("Darwin", "x86_64"); err == nil || !strings.Contains(out, "no build for darwin-amd64") {
		t.Fatalf("missing build: %v\n%s", err, out)
	}
	// A tampered binary fails the checksum.
	os.WriteFile(filepath.Join(dist, "hearth-linux-arm64"), []byte("#!/bin/sh\necho evil\n"), 0o755)
	if out, err := run("Linux", "aarch64"); err == nil || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("tampered: %v\n%s", err, out)
	}
}

// Site proxies: the one-liner falls back to the first one, install.sh tries
// them all when the server does not answer directly; unsafe values are dropped.
func TestInstallerSiteProxies(t *testing.T) {
	dir := t.TempDir()
	site := filepath.Join(dir, "site")
	os.MkdirAll(site, 0o755)
	os.WriteFile(filepath.Join(site, "net.json"), []byte(`{"proxies": ["http://proxy.internal:8188", "http://x:1; rm -rf ~", "socks5://y:2"]}`), 0o644)
	bin := filepath.Join(dir, "fake-binary")
	os.WriteFile(bin, []byte("binary-content"), 0o755)
	srv, err := New(Config{DataDir: filepath.Join(dir, "data"), SiteDir: site, Logger: log.New(io.Discard, "", 0),
		Dist: &Distribution{PublicURL: "https://hearth.internal:8787", BinaryPath: bin}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	want := "{ curl -fsSL --connect-timeout 5 https://hearth.internal:8787/install.sh || curl -fsSL -x http://proxy.internal:8188 https://hearth.internal:8787/install.sh; } | sh"
	if got := srv.InstallCommand(); got != want {
		t.Fatalf("install command\n got %s\nwant %s", got, want)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/install.sh", nil))
	if body := rec.Body.String(); !strings.Contains(body, "PROXIES='http://proxy.internal:8188'") || strings.Contains(body, "rm -rf") {
		t.Fatalf("install.sh proxies:\n%s", body)
	}
}
