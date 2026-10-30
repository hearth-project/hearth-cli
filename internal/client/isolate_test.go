package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A shared account whose ~/.codex holds this person's codex-mixin login:
// isolate moves their config and conversations into the workspace, has
// codex-mixin connect the new home, and hands ~/.codex back.
func TestIsolateCodexHome(t *testing.T) {
	en := newEnv(t)
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("CODEX_HOME", "")
	ws := filepath.Join(h, "ws")
	os.MkdirAll(ws, 0o755)
	home, err := InitWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEARTH_WORKSPACE", ws)
	if _, err := Login(home, en.url, "dev@example.com", []byte(pw), "shared"); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(h, ".codex")
	w := func(p, data string) {
		t.Helper()
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w(filepath.Join(shared, "config.toml"), "# codex-mixin managed config.\nmodel_catalog_json = \""+shared+"/model-catalogs/m.json\"\nmodel_provider = \"codex-mixin-custom\"\nmodel = \"x\"\n\n"+
		"[model_providers.codex-mixin-custom]\nbase_url = \"http://127.0.0.1:9/v1\"\nhttp_headers = { x-codex-mixin-client-key = \"oldkey\" }\n\n[mcp_servers.foo]\ncommand = \"foo\"\n")
	w(filepath.Join(shared, "config.toml.codex-mixin.absent"), "")
	w(filepath.Join(shared, "auth.json"), "{}")
	w(filepath.Join(shared, "auth.json.codex-mixin.absent"), "")
	w(filepath.Join(shared, "model-catalogs/m.json"), "{}")
	w(filepath.Join(shared, "state_5.sqlite"), "db")
	w(filepath.Join(shared, "rules/default.rules"), "allow")
	if _, err := editHooks(filepath.Join(shared, "hooks.json"), "hearth recall --hook", true); err != nil {
		t.Fatal(err)
	}
	day := "sessions/2026/10/23/"
	mine := day + "rollout-2026-10-23T10-00-00-01a16500-0000-7000-8000-000000000001.jsonl"
	theirs := day + "rollout-2026-10-23T10-00-00-01a16500-0000-7000-8000-000000000002.jsonl"
	w(filepath.Join(shared, mine), `{"type":"session_meta","payload":{"id":"x","cwd":"`+ws+`/repo"}}`+"\n")
	w(filepath.Join(shared, theirs), `{"type":"session_meta","payload":{"id":"y","cwd":"/root/jobs/someone"}}`+"\n")
	// codex-mixin: connect writes a new client key; uninstall-codex
	// removes what it wrote where nothing was before.
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	w(filepath.Join(bin, "codex-mixin"), "#!/bin/sh\necho \"$CODEX_HOME $*\" >> "+log+"\n"+
		"case \"$1\" in connect) sed -i 's/oldkey/newkey/' \"$CODEX_HOME/config.toml\";; uninstall-codex) rm \"$CODEX_HOME/config.toml\" \"$CODEX_HOME/auth.json\";; esac\n")
	os.Chmod(filepath.Join(bin, "codex-mixin"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	CodexBinary = filepath.Join(bin, "no-codex")
	t.Cleanup(func() { CodexBinary = "codex" })

	if !SharedDefaultHome(home, "codex") {
		t.Fatal("~/.codex in a workspace is the shared default")
	}
	r, err := e.IsolateAgentHome("codex", "", t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(home, "agent-homes", "codex")
	if r.To != dst || r.Sessions != 1 {
		t.Fatalf("%+v", r)
	}
	if !fileExists(filepath.Join(dst, mine)) || fileExists(filepath.Join(dst, theirs)) {
		t.Fatal("only this person's conversations move")
	}
	if fileExists(filepath.Join(dst, "state_5.sqlite")) || fileExists(filepath.Join(dst, "auth.json")) || !fileExists(filepath.Join(dst, "rules/default.rules")) {
		t.Fatal("state and codex-mixin's placeholder login stay, rules move")
	}
	if !fileExists(filepath.Join(dst, "config.toml.codex-mixin.absent")) {
		t.Fatal("codex-mixin's restore marker travels with the config (uninstall-codex works there too)")
	}
	cfg, _ := os.ReadFile(filepath.Join(dst, "config.toml"))
	for _, s := range []string{dst + "/model-catalogs/m.json", "newkey", `model = "x"`, "[mcp_servers.foo]"} {
		if !strings.Contains(string(cfg), s) {
			t.Fatalf("new config lacks %q:\n%s", s, cfg)
		}
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), dst+" connect codex --no-tui --custom-only") || !strings.Contains(string(calls), shared+" uninstall-codex --no-tui") {
		t.Fatalf("codex-mixin calls:\n%s", calls)
	}
	if fileExists(filepath.Join(shared, "config.toml")) {
		t.Fatal("the shared default still has the gateway config")
	}
	if b, _ := os.ReadFile(filepath.Join(r.Backup, "config.toml")); !strings.Contains(string(b), "oldkey") {
		t.Fatal("no backup of the shared config")
	}
	if b, _ := os.ReadFile(filepath.Join(shared, "hooks.json")); strings.Contains(string(b), "hearth") {
		t.Fatalf("hearth hooks left in the shared default: %s", b)
	}
	if !fileExists(filepath.Join(shared, theirs)) || !fileExists(filepath.Join(shared, mine)) {
		t.Fatal("conversations in the shared default are left in place")
	}
	if AgentHomeOf(home, "codex") != dst || SharedDefaultHome(home, "codex") || os.Getenv("CODEX_HOME") != dst {
		t.Fatalf("home now %s", AgentHomeOf(home, "codex"))
	}
	// Hearth processes of this person use it whatever their shell inherited.
	t.Setenv("CODEX_HOME", "/elsewhere/colleague")
	t.Setenv("HEARTH_HOME", home)
	Activate()
	if os.Getenv("CODEX_HOME") != dst {
		t.Fatalf("Activate kept the inherited CODEX_HOME %s", os.Getenv("CODEX_HOME"))
	}
	if r, err := e.IsolateAgentHome("codex", "", t.Logf); err != nil || !r.Already {
		t.Fatalf("again: %+v %v", r, err)
	}
}

func TestStripMixinConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("model_catalog_json = \"/r/.codex/model-catalogs/mixin-models.json\"\nmodel_provider = \"codex-mixin-custom\"\nmodel = \"x\"\n\n"+
		"[model_providers.codex-mixin-custom]\nname = \"Codex Mixin\"\nhttp_headers = { x-codex-mixin-client-key = \"k\" }\n\n[mcp_servers.foo]\ncommand = \"foo\"\n"), 0o600)
	if err := stripMixinConfig(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Contains(s, "codex-mixin") || strings.Contains(s, "model_catalog_json") || !strings.Contains(s, `model = "x"`) || !strings.Contains(s, "[mcp_servers.foo]") {
		t.Fatalf("%s", s)
	}
}

// A new workspace login on a shared account gets its own Codex home,
// unless ~/.codex already holds conversations of this person.
func TestPreferOwnCodexHome(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("HEARTH_AGENTS", "")
	ws := filepath.Join(h, "ws")
	os.MkdirAll(ws, 0o755)
	home, _ := InitWorkspace(ws)
	day := filepath.Join(h, ".codex/sessions/2026/10/23")
	os.MkdirAll(day, 0o700)
	os.WriteFile(filepath.Join(day, "rollout-a.jsonl"), []byte(`{"type":"session_meta","payload":{"id":"y","cwd":"/root/jobs/someone"}}`+"\n"), 0o600)
	if got := PreferOwnCodexHome(home); got != filepath.Join(home, "agent-homes", "codex") || os.Getenv("CODEX_HOME") != got {
		t.Fatalf("fresh: %q", got)
	}
	if PreferOwnCodexHome(home) != "" {
		t.Fatal("chosen once")
	}
	home2 := filepath.Join(t.TempDir(), "ws2")
	os.MkdirAll(home2, 0o755)
	h2, _ := InitWorkspace(home2)
	os.WriteFile(filepath.Join(day, "rollout-b.jsonl"), []byte(`{"type":"session_meta","payload":{"id":"z","cwd":"`+home2+`/repo"}}`+"\n"), 0o600)
	if got := PreferOwnCodexHome(h2); got != "" {
		t.Fatalf("their conversations in ~/.codex: isolate is their call, got %q", got)
	}
}
