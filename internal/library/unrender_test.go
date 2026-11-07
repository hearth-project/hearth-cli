package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnrenderKeepsEditedAndForeignFiles(t *testing.T) {
	dir := t.TempDir()
	ours := filepath.Join(dir, "ours.md")
	edited := filepath.Join(dir, "edited.md")
	link := filepath.Join(dir, "skill")
	foreign := filepath.Join(dir, "foreign.md")
	for _, p := range []string{ours, edited, foreign} {
		if err := os.WriteFile(p, []byte("hearth"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/nowhere/skill", link); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Entries: map[string]Entry{
		ours:   {Kind: "file", SHA256: hashOf([]byte("hearth"))},
		edited: {Kind: "file", SHA256: hashOf([]byte("hearth"))},
		link:   {Kind: "link", Link: "/nowhere/skill"},
	}}
	mp := filepath.Join(dir, "render.json")
	if err := m.save(mp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(edited, []byte("changed by the user"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, kept, err := Unrender(mp)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || len(kept) != 1 || kept[0] != edited {
		t.Fatalf("removed=%v kept=%v", removed, kept)
	}
	for _, p := range []string{ours, link, mp} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	for _, p := range []string{edited, foreign} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
}
