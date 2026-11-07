package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/api"
)

func TestIsDeviceRemoved(t *testing.T) {
	removed := &HTTPError{Status: 410, Body: api.Error{Error: api.DeviceRemoved}}
	if !IsDeviceRemoved(fmt.Errorf("push: %w", removed)) {
		t.Error("wrapped removal not detected")
	}
	for _, err := range []error{
		nil,
		errors.New("device removed"),
		&HTTPError{Status: 401, Body: api.Error{Error: "invalid or expired token"}},
		&HTTPError{Status: 410, Body: api.Error{Error: "session deleted"}},
	} {
		if IsDeviceRemoved(err) {
			t.Errorf("%v taken as removal", err)
		}
	}
}

func TestDecommissionTouchesOnlyHearthFiles(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("CODEX_HOME", filepath.Join(user, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(user, ".claude"))
	home := filepath.Join(user, ".hearth")
	t.Setenv("HEARTH_HOME", home)
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, "config.json"), "{}")
	write(filepath.Join(home, "vault.key"), "secret")
	write(filepath.Join(home, "memory", "notes.md"), "private")
	write(filepath.Join(home, "backup", "original.txt"), "user file")
	write(filepath.Join(home, "not-ours.txt"), "someone else")
	sshCfg := filepath.Join(user, ".ssh", "config")
	write(sshCfg, "Host mine\n  User me\n"+SSHConfigBegin+"\nHost h1\n"+SSHConfigEnd+"\nHost other\n")
	write(filepath.Join(user, ".zshrc"), "export MINE=1\n")
	write(filepath.Join(user, ".ssh", "id_ed25519"), "key")

	if _, err := Decommission(home); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"config.json", "vault.key", "memory"} {
		if _, err := os.Stat(filepath.Join(home, p)); !os.IsNotExist(err) {
			t.Errorf("%s not deleted", p)
		}
	}
	for _, p := range []string{
		filepath.Join(home, "backup", "original.txt"),
		filepath.Join(home, "not-ours.txt"),
		filepath.Join(user, ".ssh", "id_ed25519"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", p, err)
		}
	}
	b, _ := os.ReadFile(sshCfg)
	if got := string(b); strings.Contains(got, "hearth") || !strings.Contains(got, "Host mine") || !strings.Contains(got, "Host other") {
		t.Errorf("ssh config = %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(user, ".zshrc")); string(b) != "export MINE=1\n" {
		t.Errorf(".zshrc = %q", b)
	}
}
