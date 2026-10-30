package client

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A tool installed in ~/.local/bin after the daemon started is found, and
// the daemon's own PATH order is kept.
func TestEnsureUserPath(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	bin := filepath.Join(h, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho codex-cli 9.9.9\n"), 0o755)
	t.Setenv("PATH", "/usr/bin:/bin")
	EnsureUserPath()
	p := os.Getenv("PATH")
	if !strings.HasPrefix(p, "/usr/bin:/bin:") || !strings.Contains(p, bin) {
		t.Fatalf("PATH = %s", p)
	}
	EnsureUserPath()
	if strings.Count(os.Getenv("PATH"), bin) != 1 {
		t.Fatal("added twice")
	}
	if v := commandVersion(filepath.Join(bin, "codex")); v != "codex-cli 9.9.9" {
		t.Fatalf("version %q", v)
	}
}

// A proxy the daemon reached the server with is used by commands started
// from shells without one, and never overrides one that is set.
func TestRememberedProxy(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"https_proxy", "http_proxy", "no_proxy", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY"} {
		t.Setenv(k, "")
	}
	t.Setenv("https_proxy", "http://p:8188")
	RememberProxy(home)
	t.Setenv("https_proxy", "")
	useRememberedProxy(home)
	if os.Getenv("https_proxy") != "http://p:8188" {
		t.Fatalf("remembered proxy not used: %q", os.Getenv("https_proxy"))
	}
	t.Setenv("https_proxy", "http://other:1")
	useRememberedProxy(home)
	if os.Getenv("https_proxy") != "http://other:1" {
		t.Fatal("overrode the shell's proxy")
	}
}

// The login shell's PATH is read from its rc files.
func TestLoginShellPath(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("SHELL", "/bin/bash")
	os.WriteFile(filepath.Join(h, ".bash_profile"), []byte("echo noise\nexport PATH=\"/opt/from-profile/bin:$PATH\"\n"), 0o644)
	loginPath.done, loginPath.dirs = false, nil
	defer func() { loginPath.done, loginPath.dirs = false, nil }()
	found := false
	for _, d := range loginShellPath() {
		found = found || d == "/opt/from-profile/bin"
	}
	if !found {
		t.Fatalf("login PATH %v", loginPath.dirs)
	}
}

// The transport picks up a proxy set after the process started (the
// remembered one), unlike http.ProxyFromEnvironment, which reads it once.
func TestTransportProxyPerRequest(t *testing.T) {
	for _, k := range []string{"https_proxy", "http_proxy", "no_proxy", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY"} {
		t.Setenv(k, "")
	}
	tr := fastTransport()
	req, _ := http.NewRequest("GET", "https://server.example:8787/v1/info", nil)
	if u, _ := tr.Proxy(req); u != nil {
		t.Fatalf("proxy without env: %v", u)
	}
	t.Setenv("https_proxy", "http://p:8188")
	if u, _ := tr.Proxy(req); u == nil || u.Host != "p:8188" {
		t.Fatalf("proxy set later not used: %v", u)
	}
}

// A tool off PATH is found in an app bundle, as a running process, or in a
// launchd agent's program arguments.
func TestLookToolFallbacks(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("PATH", "/nonexistent")
	mk := func(rel string) string {
		p := filepath.Join(h, "Applications", rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
		return p
	}
	// Codex shipped with the ChatGPT app: the wrapper and the nested app.
	nested := mk("ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex")
	if p, err := lookTool("codex"); err != nil || p != nested {
		t.Fatalf("nested CodexCLI.app: %q %v", p, err)
	}
	wrapper := mk("ChatGPT.app/Contents/Resources/codex-cli/bin/codex")
	if p, _ := lookTool("codex"); p != wrapper {
		t.Fatalf("wrapper not preferred: %q", p)
	}
	if p, _ := lookTool("codex-mixin"); strings.Contains(p, "ChatGPT.app") {
		t.Fatalf("codex-mixin found in the ChatGPT app: %q", p)
	}
	mixin := mk("Codex Mixin.app/Contents/Resources/codex-mixin")
	if p, _ := lookTool("codex-mixin"); p != mixin {
		t.Fatalf("Codex Mixin.app: %q", p)
	}
	// Elsewhere in an app: found by walking apps named like the tool.
	os.RemoveAll(filepath.Join(h, "Applications"))
	odd := mk("Codex Beta.app/Contents/Resources/app/bin/codex")
	if p, _ := lookTool("codex"); p != odd {
		t.Fatalf("walk: %q", p)
	}
	if self, _ := os.Executable(); runningBinary(filepath.Base(self)) == "" {
		t.Fatal("running process not found")
	}
}

// A colleague's rc file sourced from ~/.bashrc exports CODEX_HOME: the
// route check finds the effective home, that it bypasses the gateway, and
// which line set it.
func TestCodexRouteSharedRC(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("CODEX_HOME", "")
	other := filepath.Join(h, "colleague")
	os.MkdirAll(filepath.Join(other, "codex_home"), 0o755)
	os.WriteFile(filepath.Join(other, "codex_home", "config.toml"), []byte("model_provider = \"openai\"\nforced_login_method = \"chatgpt\"\n"), 0o644)
	os.WriteFile(filepath.Join(other, "bashrc"), []byte("alias x=y\nexport CODEX_HOME="+filepath.Join(other, "codex_home")+"\n"), 0o644)
	os.WriteFile(filepath.Join(h, ".bashrc"), []byte("echo hi\n. "+filepath.Join(other, "bashrc")+"\n"), 0o644)
	os.MkdirAll(filepath.Join(h, ".codex"), 0o755)
	os.WriteFile(filepath.Join(h, ".codex", "config.toml"), []byte("model_provider = \"codex-mixin-custom\"\n[model_providers.codex-mixin-custom]\nbase_url = \"http://127.0.0.1:9/v1\"\n"), 0o644)
	home := filepath.Join(h, "ws", ".hearth")
	os.MkdirAll(home, 0o700)
	if got := loginCodexHome(home, ""); got != filepath.Join(other, "codex_home") {
		t.Fatalf("effective CODEX_HOME %q", got)
	}
	if p, base := codexConfigRoute(filepath.Join(other, "codex_home")); p != "openai" || isLoopbackURL(base) {
		t.Fatalf("route %s %s", p, base)
	}
	by := codexHomeSetBy(home, "")
	if !strings.Contains(by, filepath.Join(other, "bashrc")+":2") || !strings.Contains(by, ".bashrc:2") {
		t.Fatalf("set by %q", by)
	}
	if p, base := codexConfigRoute(filepath.Join(h, ".codex")); p != "codex-mixin-custom" || !isLoopbackURL(base) {
		t.Fatalf("own route %s %s", p, base)
	}
	// On the shared account (a workspace) the block at the end of the rc
	// file gives this person's sessions their own Codex home again.
	os.MkdirAll(filepath.Join(h, "ws"), 0o755)
	if _, err := InitWorkspace(filepath.Join(h, "ws")); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureIdentityRC(home, "me@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := loginCodexHome(home, "me@example.com"); got != filepath.Join(h, ".codex") {
		t.Fatalf("own session CODEX_HOME %q", got)
	}
	if got := loginCodexHome(home, "colleague@example.com"); got != filepath.Join(other, "codex_home") {
		t.Fatalf("a colleague's session changed: %q", got)
	}
	// Lines appended after the block later: it moves back to the end.
	f, _ := os.OpenFile(filepath.Join(h, ".bashrc"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("export CODEX_HOME=/elsewhere\n")
	f.Close()
	if ch, _ := EnsureIdentityRC(home, "me@example.com"); len(ch) == 0 {
		t.Fatal("block not moved to the end")
	}
	if got := loginCodexHome(home, "me@example.com"); got != filepath.Join(h, ".codex") {
		t.Fatalf("after appended lines: %q", got)
	}
}
