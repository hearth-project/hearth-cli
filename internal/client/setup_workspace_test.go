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
