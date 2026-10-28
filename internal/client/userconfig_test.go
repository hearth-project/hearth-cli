package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The per-feature files of older versions are folded into config.toml once.
func TestMigrateUserConfig(t *testing.T) {
	isolateAgentHomes(t)
	home := t.TempDir()
	lib := LibraryDir(home)
	os.MkdirAll(filepath.Join(lib, "skills/s1"), 0o755)
	os.WriteFile(filepath.Join(lib, "skills/s1/SKILL.md"), []byte("---\nname: s1\ndescription: d\n---\nx\n"), 0o644)
	os.WriteFile(filepath.Join(lib, "agents.json"), []byte(`{"enabled":["codex"],"homes":{"codex":"${WORKSPACE}/.codex"}}`), 0o644)
	os.WriteFile(filepath.Join(lib, "resident.txt"), []byte("s1\n"), 0o644)
	os.WriteFile(filepath.Join(lib, "backup.conf"), []byte("@codex\n@codex.sessions\n!@claude.sessions\n!**/*.tmp\n"), 0o644)
	os.WriteFile(filepath.Join(lib, "tools.json"), []byte(`{"tools":[{"name":"codex","version":"1.2.3"}]}`), 0o644)
	os.WriteFile(filepath.Join(lib, "learn-ignore.json"), []byte(`{"ignore":["x"]}`), 0o644)
	os.WriteFile(filepath.Join(lib, "workspaces.json"), []byte(`{"roots":["~/work"],"max_depth":2}`), 0o644)
	os.WriteFile(filepath.Join(lib, "mcp.toml"), []byte("[servers.fs]\ncommand = \"npx\"\n"), 0o644)

	if _, err := EnsureUserConfig(home); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(lib, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"enabled = ['codex']", "s1", "'claude.sessions' = false", "**/*.tmp", "1.2.3", "ignore = ['x']", "[mcp.servers.fs]"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("config.toml missing %q:\n%s", want, b)
		}
	}
	for _, old := range []string{"agents.json", "resident.txt", "backup.conf", "tools.json", "learn-ignore.json", "workspaces.json", "mcp.toml"} {
		if _, err := os.Stat(filepath.Join(lib, old)); err == nil {
			t.Fatalf("%s left behind", old)
		}
	}
	// Readers use the unified file.
	if a := EnabledAgents(home); len(a) != 1 || a[0] != "codex" {
		t.Fatalf("agents %v", a)
	}
	if !BackupSelection(home).Denied["claude.sessions"] {
		t.Fatal("backup exclude not read")
	}
	m, err := LoadTools(home)
	if err != nil || len(m.Tools) != 1 || m.Tools[0].Version != "1.2.3" {
		t.Fatalf("tools %+v %v", m, err)
	}
	if cfg, present, err := LoadMCPConfig(home); err != nil || !present || cfg.Servers["fs"].Command != "npx" {
		t.Fatalf("mcp %+v %v", cfg, err)
	}
}

// Writes through the commands land in config.toml, not in split files.
func TestWritersUseConfigToml(t *testing.T) {
	isolateAgentHomes(t)
	home := t.TempDir()
	if err := SetAgentEnabled(home, "claude", true); err != nil {
		t.Fatal(err)
	}
	if err := SetBackupCategory(home, "claude", false); err != nil {
		t.Fatal(err)
	}
	if _, err := AddBackupRule(home, "~/.ssh/id_rsa", true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(LibraryDir(home), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"enabled = ['codex', 'claude']", "claude = false", "exclude = ['~/.ssh/id_rsa']"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("config.toml missing %q:\n%s", want, b)
		}
	}
	for _, old := range []string{"agents.json", "backup.conf"} {
		if _, err := os.Stat(filepath.Join(LibraryDir(home), old)); err == nil {
			t.Fatalf("%s written next to config.toml", old)
		}
	}
}
