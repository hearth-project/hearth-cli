package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/skills"
)

// Bundled skills go into every agent present on the machine, are updated
// in place, and never overwrite a skill the user wrote.
func TestInstallSkillsForAllAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HEARTH_HOME", filepath.Join(home, ".hearth")) // never the developer's real library
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("PATH", t.TempDir()) // no agent CLIs
	for _, d := range []string{".codex", ".claude", ".comate"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	mine := filepath.Join(home, ".claude", "skills", "hearth", "SKILL.md")
	os.MkdirAll(filepath.Dir(mine), 0o755)
	os.WriteFile(mine, []byte("my own skill"), 0o644)
	// An old hearth-setup installed by hearth is removed (merged into hearth).
	old := filepath.Join(home, ".codex", "skills", "hearth-setup", "SKILL.md")
	os.MkdirAll(filepath.Dir(old), 0o755)
	os.WriteFile(old, []byte("---\nname: hearth-setup\n---\n\n# Hearth setup\n"), 0o644)

	if err := installSetupSkill(false); err != nil {
		t.Fatal(err)
	}
	if len(skills.All()) != 1 || skills.All()[0].Name != "hearth" {
		t.Fatalf("bundled skills: %+v", skills.All())
	}
	for _, agent := range []string{".codex", ".claude", ".comate"} {
		for _, sk := range skills.All() {
			p := filepath.Join(home, agent, "skills", sk.Name, "SKILL.md")
			b, err := os.ReadFile(p)
			if p == mine {
				if string(b) != "my own skill" {
					t.Fatal("overwrote the user's skill")
				}
				continue
			}
			if err != nil || !strings.Contains(string(b), skillMarker) || !strings.HasPrefix(string(b), "---\nname: "+sk.Name) {
				t.Fatalf("%s: %v", p, err)
			}
		}
	}
	if _, err := os.Stat(filepath.Dir(old)); err == nil {
		t.Fatal("retired hearth-setup skill not removed")
	}
	// A retired skill symlinked to the user's own copy: the copy survives.
	repo := filepath.Join(t.TempDir(), "hearth-setup")
	os.MkdirAll(repo, 0o755)
	os.WriteFile(filepath.Join(repo, "SKILL.md"), []byte("---\nname: hearth-setup\n---\n\n# Hearth setup\n"), 0o644)
	os.Symlink(repo, filepath.Join(home, ".comate", "skills", "hearth-setup"))
	installSetupSkill(false)
	if _, err := os.Stat(filepath.Join(repo, "SKILL.md")); err != nil {
		t.Fatal("deleted the user's copy through a symlink")
	}
	// An older hearth-installed copy is updated.
	p := filepath.Join(home, ".codex", "skills", "hearth", "SKILL.md")
	os.WriteFile(p, []byte("old\n"+skillMarker+"\n"), 0o644)
	installSetupSkill(false)
	if b, _ := os.ReadFile(p); strings.HasPrefix(string(b), "old") {
		t.Fatal("hearth-installed skill not updated")
	}
	// No agent at all: nothing written.
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	installSetupSkill(false)
	if _, err := os.Stat(filepath.Join(empty, ".codex")); err == nil {
		t.Fatal("created an agent directory that did not exist")
	}
}
