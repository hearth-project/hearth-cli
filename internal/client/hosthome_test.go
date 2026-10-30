package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// A workspace on a disk several machines mount: the machine that logged in
// there first owns <ws>/.hearth, the others get <ws>/.hearth/hosts/<host>.
func TestWorkspaceHomeOnSharedDisk(t *testing.T) {
	ws := t.TempDir()
	base := filepath.Join(ws, ".hearth")
	if got := WorkspaceHome(ws); got != base {
		t.Fatalf("free workspace: %s", got)
	}
	os.MkdirAll(base, 0o700)
	saveJSON(filepath.Join(base, workspaceMarker), workspaceFile{Path: ws})
	// Logged in by this machine (legacy home without the marker).
	saveJSON(filepath.Join(base, "config.json"), Config{DeviceName: hostKey(), Email: "a@x"})
	if got := WorkspaceHome(ws); got != base {
		t.Fatalf("this machine's legacy home: %s", got)
	}
	// Another machine of the cluster logged in there.
	os.WriteFile(filepath.Join(base, hostMarker), []byte("gpu-node-7\n"), 0o600)
	own := filepath.Join(base, "hosts", hostKey())
	if got := WorkspaceHome(ws); got != own {
		t.Fatalf("other machine's home: %s", got)
	}
	if dev, other := OtherHostHome(ws); !other || dev != hostKey() {
		t.Fatalf("other host: %q %v", dev, other)
	}
	home, err := InitWorkspace(ws)
	if err != nil || home != own || WorkspaceOf(own) != ws {
		t.Fatalf("init: %s %v (workspace %q)", home, err, WorkspaceOf(own))
	}
	t.Setenv("HEARTH_HOME", "")
	sub := filepath.Join(ws, "repo")
	os.MkdirAll(sub, 0o755)
	wd, _ := os.Getwd()
	os.Chdir(sub)
	defer os.Chdir(wd)
	if got := Home(); got != own {
		t.Fatalf("Home() inside the workspace: %s", got)
	}
	// A login made here claims it for this machine.
	if err := SaveConfig(own, &Config{DeviceName: "named-by-user", Email: "a@x"}); err != nil {
		t.Fatal(err)
	}
	if !homeOfThisHost(own) {
		t.Fatal("own home not claimed")
	}
	// Moving a ~/.hearth login into the workspace lands in the per-host home.
	from := filepath.Join(t.TempDir(), ".hearth")
	os.MkdirAll(from, 0o700)
	os.RemoveAll(own)
	saveJSON(filepath.Join(from, "config.json"), Config{DeviceName: hostKey(), Email: "a@x"})
	got, err := MoveHome(from, ws)
	if err != nil || got != own {
		t.Fatalf("move: %s %v", got, err)
	}
	if _, err := LoadConfig(own); err != nil {
		t.Fatal("login not moved")
	}
}

// Moving a login moves the rc-file blocks that sourced its scripts.
func TestMoveHomeRehomesRC(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	from := filepath.Join(h, ".hearth")
	os.MkdirAll(filepath.Join(from, "shell"), 0o700)
	hook := filepath.Join(from, "shell", "bashrc.hook.sh")
	os.WriteFile(hook, []byte("echo hi\n"), 0o600)
	saveJSON(filepath.Join(from, "config.json"), Config{DeviceName: hostKey(), Email: "a@x"})
	rc := filepath.Join(h, ".bashrc")
	os.WriteFile(rc, []byte("alias x=y\n"), 0o644)
	if _, err := recipe.InstallRC(rc, hook); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(from, "opt", "node", "bin"), 0o755)
	os.WriteFile(filepath.Join(from, "opt", "node", "bin", "codex"), []byte("#!/bin/sh\n"), 0o755)
	ws := filepath.Join(h, "ws")
	to, err := MoveHome(from, ws)
	if err != nil {
		t.Fatal(err)
	}
	// Tools installed under the home stay where their wrappers point.
	if !fileExists(filepath.Join(from, "opt", "node", "bin", "codex")) || fileExists(filepath.Join(to, "opt")) {
		t.Fatal("opt/ moved with the login")
	}
	b, _ := os.ReadFile(rc)
	if strings.Contains(string(b), hook) || !strings.Contains(string(b), filepath.Join(to, "shell", "bashrc.hook.sh")) || !strings.Contains(string(b), "alias x=y") {
		t.Fatalf("rc after the move:\n%s", b)
	}
}
