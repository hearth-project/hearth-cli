package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An on-demand machine writes a skill's credentials when asked, does not
// upload them, and removing them (expired) deletes nothing on the server.
func TestUseCredentialsOnDemand(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	libWrite(t, a, "skills/bos/SKILL.md", "---\nname: bos\ndescription: d\n---\nupload\n")
	libWrite(t, a, "skills/bos/assets/credentials", "[Defaults]\nAk = 0123456789abcdef0123\n")
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "tok")
	if err := a.SetSecret(Secret{Name: "ugate", Value: []byte("tok-1"), RestoreTo: f}); err != nil {
		t.Fatal(err)
	}
	SetOnDemand(b.Home, true)
	if _, err := b.LibAuto(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(LibraryDir(b.Home), "skills/bos/assets/credentials")
	us, err := b.UseCredentials([]string{"bos", "ugate"}, time.Hour)
	if err != nil || len(us) != 2 {
		t.Fatalf("%+v %v", us, err)
	}
	for _, u := range us {
		if u.Action != "written" {
			t.Fatalf("%+v", u)
		}
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("skill credential: %v", err)
	}
	// Again: present, nothing rewritten; an unknown name is reported.
	if us, err := b.UseCredentials([]string{"bos", "nope"}, time.Hour); err == nil || !strings.Contains(err.Error(), "nope") || us[0].Action != "present" {
		t.Fatalf("%+v %v", us, err)
	}
	// b syncs: the file is not pushed from here, and its removal deletes nothing.
	libWrite(t, b, "skills/other/SKILL.md", "---\nname: other\ndescription: d\n---\nx\n")
	if _, err := b.LibAuto(); err != nil {
		t.Fatal(err)
	}
	if removed, _ := RemoveTempCredentials(b.Home, false, nil); len(removed) != 0 {
		t.Fatal("removed before its time")
	}
	if removed, _ := RemoveTempCredentials(b.Home, true, nil); len(removed) != 2 || fileExists(p) || fileExists(f) {
		t.Fatalf("removed %v", removed)
	}
	if _, err := b.LibAuto(); err != nil {
		t.Fatal(err)
	}
	ss, _ := a.Secrets()
	n := 0
	for _, s := range ss {
		if s.RestoreTo == libRestorePrefix+"skills/bos/assets/credentials" {
			n++
		}
	}
	if n != 1 {
		t.Fatal("the skill's credential secret was deleted")
	}
	// A file changed after it was written is the user's: kept.
	b.UseCredentials([]string{"ugate"}, time.Hour)
	os.WriteFile(f, []byte("mine now"), 0o600)
	if removed, _ := RemoveTempCredentials(b.Home, true, nil); len(removed) != 0 || !fileExists(f) {
		t.Fatal("a changed file was removed")
	}
}
