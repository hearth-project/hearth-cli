package client

import (
	"path/filepath"
	"testing"
)

// An on-demand machine writes no credential files of skills.
func TestOnDemandMachine(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	libWrite(t, a, "skills/bos/SKILL.md", "---\nname: bos\ndescription: d\n---\nupload\n")
	libWrite(t, a, "skills/bos/assets/credentials", "[Defaults]\nAk = 0123456789abcdef0123\n")
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}
	if err := SetOnDemand(b.Home, true); err != nil || !OnDemand(b.Home) {
		t.Fatalf("mode: %v", err)
	}
	if _, err := b.LibAuto(); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(LibraryDir(b.Home), "skills/bos/assets/credentials")) {
		t.Fatal("credential written on an on-demand machine")
	}
	// b pushes a change of its own: a's credential secret survives.
	libWrite(t, b, "skills/other/SKILL.md", "---\nname: other\ndescription: d\n---\nx\n")
	if _, err := b.LibAuto(); err != nil {
		t.Fatal(err)
	}
	if fs := LibSecretFiles(a.Home); len(fs) != 1 {
		t.Fatalf("a's credential files: %v", fs)
	}
	ss, _ := a.Secrets()
	n := 0
	for _, s := range ss {
		if s.RestoreTo == libRestorePrefix+"skills/bos/assets/credentials" {
			n++
		}
	}
	if n != 1 {
		t.Fatal("the on-demand machine deleted the credential secret")
	}
}
