package mcpcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lookup(m map[string]string) SecretLookup {
	return func(n string) ([]byte, bool, error) { v, ok := m[n]; return []byte(v), ok, nil }
}

const cfgTOML = `
[servers.memory]
command = "npx"
args = ["--registry=http://registry.npm.example.com", "-y", "@modelcontextprotocol/server-memory"]
env = { MEMORY_FILE_PATH = "~/.hearth/memory/graph.json" }

[servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
env = { GITHUB_PERSONAL_ACCESS_TOKEN = "${secret:GITHUB_PAT}" }
agents = ["comate"]
`

func TestRenderJSONMergesAndProtects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := filepath.Join(home, "lib/mcp.toml")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(cfgTOML), 0o644)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	comate := filepath.Join(home, ".comate/mcp.json")
	claude := filepath.Join(home, ".claude.json")
	os.MkdirAll(filepath.Dir(comate), 0o700)
	os.WriteFile(comate, []byte(`{"mcpServers":{"mine":{"command":"x"},"memory":{"command":"old"}}}`), 0o600)
	os.WriteFile(claude, []byte(`{"numStartups":7,"projects":{"/a":{}}}`), 0o600)
	ts := []Target{{Agent: "comate", JSONPath: comate}, {Agent: "claude", JSONPath: claude}}
	st := State{}

	acts, err := Render(cfg, ts, lookup(map[string]string{"GITHUB_PAT": "ghp_x"}), st, false)
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]string{}
	for _, a := range acts {
		ops[a.Agent+"/"+a.Server] = a.Op
	}
	if ops["comate/memory"] != "skip" || ops["comate/github"] != "create" || ops["claude/memory"] != "create" || ops["claude/github"] != "" {
		t.Fatalf("ops: %v", ops)
	}
	var doc map[string]any
	b, _ := os.ReadFile(claude)
	json.Unmarshal(b, &doc)
	if doc["numStartups"] != float64(7) || doc["projects"] == nil {
		t.Fatalf("other claude.json keys lost: %s", b)
	}
	if !strings.Contains(string(b), home+"/.hearth/memory/graph.json") {
		t.Fatalf("~ not expanded: %s", b)
	}
	b, _ = os.ReadFile(comate)
	if !strings.Contains(string(b), `"mine"`) || !strings.Contains(string(b), "ghp_x") || !strings.Contains(string(b), `"old"`) {
		t.Fatalf("comate merge: %s", b)
	}
	if fi, _ := os.Stat(comate); fi.Mode().Perm() != 0o600 {
		t.Fatal("mode changed")
	}

	// --adopt replaces; removing a server from the config removes only ours.
	Render(cfg, ts, lookup(map[string]string{"GITHUB_PAT": "ghp_x"}), st, true)
	delete(cfg.Servers, "github")
	acts, _ = Render(cfg, ts, lookup(nil), st, false)
	b, _ = os.ReadFile(comate)
	if strings.Contains(string(b), "github") || !strings.Contains(string(b), `"mine"`) || strings.Contains(string(b), `"old"`) {
		t.Fatalf("after removal: %s", b)
	}

	// Missing secret: reported, file untouched for that server.
	cfg.Servers["github"] = Server{Command: "npx", Env: map[string]string{"T": "${secret:NOPE}"}}
	acts, _ = Render(cfg, ts[:1], lookup(nil), st, false)
	if acts[0].Op != "failed" || !strings.Contains(acts[0].Detail, "NOPE") {
		t.Fatalf("missing secret: %+v", acts)
	}
}

func TestRenderCodexViaCLI(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	reg := filepath.Join(dir, "registered")
	os.Mkdir(reg, 0o755)
	// Fake codex: mcp get/add/remove keep one marker file per server.
	bin := filepath.Join(dir, "codex")
	os.WriteFile(bin, []byte(`#!/bin/sh
echo "$@" >> `+log+`
case "$2" in
  get) test -f `+reg+`/"$3" ;;
  remove) rm -f `+reg+`/"$3" ;;
  add) while [ "$1" != "--" ]; do last=$1; shift; done; touch `+reg+`/"$last" ;;
esac
`), 0o755)
	cfg := &Config{Servers: map[string]Server{"memory": {Command: "npx", Args: []string{"-y", "pkg"}, Env: map[string]string{"A": "1"}}}}
	st := State{}
	ts := []Target{{Agent: "codex", CodexBin: bin}}
	if acts, err := Render(cfg, ts, lookup(nil), st, false); err != nil || acts[0].Op != "create" {
		t.Fatalf("%+v %v", acts, err)
	}
	if acts, _ := Render(cfg, ts, lookup(nil), st, false); acts[0].Op != "unchanged" {
		t.Fatalf("second: %+v", acts)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "mcp add --env A=1 memory -- npx -y pkg") {
		t.Fatalf("codex args:\n%s", b)
	}
	cfg.Servers = map[string]Server{}
	Render(cfg, ts, lookup(nil), st, false)
	if _, err := os.Stat(filepath.Join(reg, "memory")); !os.IsNotExist(err) {
		t.Fatal("stale codex server not removed")
	}
}

func TestImportJSONExtractsSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mcp.json")
	os.WriteFile(p, []byte(`{"mcpServers":{"github":{"command":"npx","args":["-y","gh"],"env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"ghp_secret","npm_config_registry":"http://r"}}}}`), 0o600)
	servers, secrets, err := ImportJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	if secrets["GITHUB_PERSONAL_ACCESS_TOKEN"] != "ghp_secret" || servers["github"].Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "${secret:GITHUB_PERSONAL_ACCESS_TOKEN}" ||
		servers["github"].Env["npm_config_registry"] != "http://r" {
		t.Fatalf("%+v %v", servers, secrets)
	}
}
