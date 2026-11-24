package client

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Credential files of skills that left the library expire after
// LibSecretTTL; those of skills still here, recent ones (another machine
// may be about to push the skill) and other secrets stay.
func TestPruneLibSecrets(t *testing.T) {
	en := newEnv(t)
	m := en.machine("box")
	skills := library.Library{Root: LibraryDir(m.Home)}.SkillsDir()
	if err := os.MkdirAll(filepath.Join(skills, "kept"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, to := range map[string]string{
		"lib-kept":   "library:skills/kept/token",
		"lib-gone":   "library:skills/gone/token",
		"lib-escape": "library:skills/../token",
		"plain":      "~/.config/x/token",
	} {
		if err := m.SetSecret(Secret{Name: name, Value: []byte("v"), RestoreTo: to}); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { libSecretNow = time.Now }()
	libSecretNow = func() time.Time { return time.Now().Add(LibSecretTTL - time.Hour) }
	if n, err := m.PruneLibSecrets(); err != nil || n != 0 {
		t.Fatalf("pruned before the TTL: %d %v", n, err)
	}
	libSecretNow = func() time.Time { return time.Now().Add(LibSecretTTL + time.Hour) }
	if n, err := m.PruneLibSecrets(); err != nil || n != 1 {
		t.Fatalf("pruned %d, want 1: %v", n, err)
	}
	all, err := m.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	for _, s := range all {
		left[s.Name] = true
	}
	if left["lib-gone"] || !left["lib-kept"] || !left["lib-escape"] || !left["plain"] {
		t.Fatalf("left after pruning: %v", left)
	}
}
