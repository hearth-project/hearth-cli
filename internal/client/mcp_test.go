package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/mcpcfg"
)

func TestMCPImportSyncsAcrossMachines(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	src := filepath.Join(t.TempDir(), "mcp.json")
	os.WriteFile(src, []byte(`{"mcpServers":{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"],
		"env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"ghp_REALTOKEN1234567890","npm_config_registry":"http://registry.npm.example.com"}}}}`), 0o600)
	if _, _, err := a.ImportMCP(src, false); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(LibraryDir(a.Home), "config.toml"))
	if strings.Contains(string(cfg), "ghp_REALTOKEN") || !strings.Contains(string(cfg), "${secret:GITHUB_PERSONAL_ACCESS_TOKEN}") {
		t.Fatalf("token left in config.toml:\n%s", cfg)
	}
	if _, err := a.LibPush(false); err != nil {
		t.Fatalf("library with mcp in config.toml must pass the secret scan: %v", err)
	}
	b.libTargets(t)
	dst := filepath.Join(t.TempDir(), ".comate/mcp.json")
	b.MCPTargets = []mcpcfg.Target{{Agent: "comate", JSONPath: dst}}
	if _, err := b.LibPull(false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Render(false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !strings.Contains(string(got), "ghp_REALTOKEN1234567890") || !strings.Contains(string(got), "server-github") {
		t.Fatalf("rendered mcp.json:\n%s", got)
	}
}
