package client

import (
	"os"
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
