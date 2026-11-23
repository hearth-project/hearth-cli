package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/client"
)

func TestExistingSetupWorkspaceDoesNotGuess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	const server, email = "https://hearth.example.org", "user@example.org"
	add := func(id string) string {
		ws := t.TempDir()
		home, err := client.InitWorkspace(ws)
		if err != nil {
			t.Fatal(err)
		}
		cfg := &client.Config{Server: server, Email: email, DeviceID: id}
		if err := client.SaveConfig(home, cfg); err != nil {
			t.Fatal(err)
		}
		client.RememberSetupWorkspace(home, cfg)
		return ws
	}
	ws := add("one")
	for _, tc := range []struct{ defaultLogin, proof bool }{{false, false}, {true, true}} {
		if got, err := existingSetupWorkspace(server, email, tc.defaultLogin, tc.proof, nil); got != nil || err == nil || !strings.Contains(err.Error(), ws) || !strings.Contains(err.Error(), "--workspace") {
			t.Fatalf("expected workspace choice; got %v", err)
		}
	}
	if got, err := existingSetupWorkspace(server, email, false, true, nil); err != nil || got == nil || got.Workspace != ws {
		t.Fatalf("single proven candidate: %v", err)
	}
	// With both the old workspace and the default login, the user may choose
	// the workspace in place. Blank input must never pick either identity.
	for _, tc := range []struct{ input, want string }{{"1", ws}, {"2", ""}, {"", "invalid"}} {
		m, err := existingSetupWorkspace(server, email, true, true, func(options []string) (string, bool) {
			if len(options) != 2 || !strings.Contains(options[0], ws) || !strings.Contains(options[1], "current home") {
				t.Fatal("unexpected workspace choices")
			}
			return tc.input, true
		})
		if tc.want == "invalid" {
			if err == nil || m != nil {
				t.Fatal("selected a workspace without confirmation")
			}
		} else if err != nil || m == nil || m.Workspace != tc.want {
			t.Fatal("did not honor workspace selection")
		}
	}
	ws2 := add("two")
	if got, err := existingSetupWorkspace(server, email, false, true, nil); got != nil || err == nil || !strings.Contains(err.Error(), ws) || !strings.Contains(err.Error(), ws2) {
		t.Fatalf("expected both workspaces; got %v", err)
	}
	if _, err := os.Stat(filepath.Join(client.DefaultHome(), "config.json")); !os.IsNotExist(err) {
		t.Fatal("discovery created a login")
	}
}

// A legacy workspace without a discovery hint can still be chosen manually.
// If it is occupied, setup must stop rather than start sync in the fallback home.
func TestMoveSetupHomeRefusesOccupiedWorkspace(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("HEARTH_HOME", filepath.Join(h, ".hearth"))
	ws := t.TempDir()
	existing, err := client.InitWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SaveConfig(existing, &client.Config{DeviceID: "original"}); err != nil {
		t.Fatal(err)
	}
	if got, err := moveSetupHome(&setupOut{}, client.DefaultHome(), h, ws, ""); got != "" || err == nil || !strings.Contains(err.Error(), "before starting background sync") {
		t.Fatalf("unexpected fallback: %q, %v", got, err)
	}
	if cfg, err := client.LoadConfig(existing); err != nil || cfg.DeviceID != "original" {
		t.Fatal("existing login changed")
	}
}
