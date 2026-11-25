package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/recipe"
)

func TestSetupWorkspaces(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	const server = "https://hearth.example.org"
	const email = "user@example.org"
	rc := filepath.Join(h, ".bashrc")
	// The shared base belongs to a different host. Both identity scripts can
	// appear in the same shell file. Only the current host's login qualifies.
	ws := t.TempDir()
	base := filepath.Join(ws, ".hearth")
	if _, err := InitWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	other := &Config{DeviceID: "other", DeviceName: "elsewhere", Email: email, Server: server}
	if err := SaveConfig(base, other); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, hostMarker), []byte("elsewhere\n"), 0600); err != nil {
		t.Fatal(err)
	}
	own, err := InitWorkspace(ws)
	if err != nil || own == base {
		t.Fatalf("host home: %q %v", own, err)
	}
	cfg := &Config{DeviceID: "mine", DeviceName: "custom-name", Email: email, Server: server}
	if err := SaveConfig(own, cfg); err != nil {
		t.Fatal(err)
	}
	for _, home := range []string{base, own} {
		if _, err := recipe.InstallRC(rc, identityScript(home)); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		ms := SetupWorkspaces(server+"/", "User@example.org")
		if len(ms) != 1 || ms[0].Home != own || ms[0].Workspace != ws || ms[0].Config.DeviceID != "mine" {
			t.Fatal("wrong workspace match")
		}
		if len(SetupWorkspaces("https://other.example.org", email))+len(SetupWorkspaces(server, "other@example.org")) != 0 {
			t.Fatal("cross-account or cross-server match")
		}
	}
	check() // an old installation, with only rc markers
	RememberSetupWorkspace(own, cfg)
	check() // both hints identify the same device
	if err := os.Remove(rc); err != nil {
		t.Fatal(err)
	}
	check() // SSH and identity blocks are not required for future installs
	if err := os.Remove(filepath.Join(own, "config.json")); err != nil {
		t.Fatal(err)
	}
	if len(SetupWorkspaces(server, email)) != 0 {
		t.Fatal("stale hint matched")
	}
}

// Switching away from another person's ~/.hearth drops the agent homes
// Activate exported from it, and keeps the ones the user set.
func TestLeaveHome(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	other := DefaultHome()
	if err := SetAgentHome(other, "codex", filepath.Join(h, "colleague-codex")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "/mine/claude")
	t.Setenv("HEARTH_WORKSPACE", "/colleague/ws")
	t.Setenv("HEARTH_WORKSPACE_FROM_PROFILE", "1")
	os.Unsetenv("CODEX_HOME")
	t.Setenv("HEARTH_HOME", other)
	Activate()
	if os.Getenv("CODEX_HOME") != filepath.Join(h, "colleague-codex") {
		t.Fatalf("Activate did not export the colleague's codex home: %q", os.Getenv("CODEX_HOME"))
	}
	LeaveHome(other)
	if v := os.Getenv("CODEX_HOME"); v != "" {
		t.Fatalf("CODEX_HOME kept: %q", v)
	}
	if os.Getenv("HEARTH_WORKSPACE") != "" || os.Getenv("HEARTH_WORKSPACE_FROM_PROFILE") != "" {
		t.Fatal("profile workspace kept")
	}
	if os.Getenv("CLAUDE_CONFIG_DIR") != "/mine/claude" {
		t.Fatal("the user's own setting removed")
	}
}

// One workspace directory several people use on one machine: the first
// keeps <ws>/.hearth, each other person gets <ws>/.hearth/users/<email>,
// and HEARTH_USER picks whose home a command uses.
func TestWorkspaceHomesPerPerson(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HEARTH_USER", "")
	ws := t.TempDir()
	base, err := InitWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	login := func(home, email string) {
		t.Helper()
		if err := SaveConfig(home, &Config{Server: "https://h.example.org", Email: email, DeviceID: email, DeviceName: hostname()}); err != nil {
			t.Fatal(err)
		}
	}
	login(base, "a@example.org")
	if got := WorkspaceHomeFor(ws, "A@example.org"); got != base {
		t.Fatalf("a's home: %s", got)
	}
	bHome := WorkspaceHomeFor(ws, "b@example.org")
	if bHome != filepath.Join(ws, ".hearth", "users", "b@example.org") {
		t.Fatalf("b's new home: %s", bHome)
	}
	if h, err := InitWorkspaceHome(ws, bHome); err != nil || h != bHome || WorkspaceOf(bHome) != ws {
		t.Fatalf("init b: %s %v", h, err)
	}
	login(bHome, "b@example.org")
	if _, err := InitWorkspaceHome(ws, t.TempDir()); err == nil {
		t.Fatal("a home outside the workspace accepted")
	}
	if got := WorkspacePeople(ws); len(got) != 2 || got[0] != "a@example.org" || got[1] != "b@example.org" {
		t.Fatalf("people: %v", got)
	}
	if WorkspaceHomeHere(ws) != base {
		t.Fatal("without HEARTH_USER the first person's home")
	}
	t.Setenv("HEARTH_USER", "b@example.org")
	if WorkspaceHomeHere(ws) != bHome {
		t.Fatal("HEARTH_USER not honoured")
	}
	t.Setenv("HEARTH_USER", "")
	// A third person's login moved in (setup's workspace prompt).
	from := filepath.Join(t.TempDir(), ".hearth")
	login(from, "c@example.org")
	cHome, err := MoveHome(from, ws)
	if err != nil || cHome != filepath.Join(ws, ".hearth", "users", "c@example.org") || WorkspaceOf(cHome) != ws {
		t.Fatalf("move c: %s %v", cHome, err)
	}
	if cfg, err := LoadConfig(base); err != nil || cfg.Email != "a@example.org" {
		t.Fatal("a's login touched")
	}
	for _, h := range []string{base, bHome, cHome} {
		if !InWorkspaceState(ws, h) {
			t.Fatalf("%s not in the workspace state", h)
		}
	}
	if InWorkspaceState(ws, filepath.Join(ws, ".hearthx")) || otherWorkspace(ws, bHome) {
		t.Fatal("InWorkspaceState / otherWorkspace")
	}
}
