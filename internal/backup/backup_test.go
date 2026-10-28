package backup

import (
	"path/filepath"
	"testing"
)

func TestSelection(t *testing.T) {
	c := Parse(`
@sessions
# @credentials
@memory  # notes
~/.ssh/config
~/.ssh/id_*
${WORKSPACE}/notes/
docs/*.md
!~/.ssh/id_*_old
!**/*.tmp
`, "/home/u", "/ws")
	if !c.Enabled(Sessions) || !c.Enabled(Memory) || !c.Enabled(Credentials) || !c.Enabled(Workspace) {
		t.Fatalf("unmentioned categories must default to on: %v", c.Categories)
	}
	for p, want := range map[string]bool{
		"/home/u/.ssh/config":     true,
		"/home/u/.ssh/id_ed25519": true,
		"/home/u/.ssh/id_rsa_old": false,
		"/home/u/.ssh/known":      false,
		"/ws/notes/a/b.txt":       true,
		"/ws/notes/x.tmp":         false,
		"/ws/docs/readme.md":      true,
		"/ws/docs/sub/readme.md":  false,
	} {
		if got := c.Included(p); got != want {
			t.Errorf("%s: %v, want %v", p, got, want)
		}
	}
	roots := map[string]bool{}
	for _, r := range c.Roots() {
		roots[r] = true
	}
	for _, r := range []string{"/home/u/.ssh/config", "/home/u/.ssh", "/ws/notes", "/ws/docs"} {
		if !roots[r] {
			t.Errorf("missing root %s in %v", r, c.Roots())
		}
	}
}

func TestDefaultAndEdits(t *testing.T) {
	p := filepath.Join(t.TempDir(), FileName)
	c, exists, err := Load(p, "/h", "/w")
	if err != nil || exists || !c.Selected("codex.sessions", "codex", "sessions") || !c.Enabled(Credentials) || !c.Categories["codex.memory"] {
		t.Fatalf("default: %v %v %v", c.Categories, exists, err)
	}
	if err := SetCategory(p, Credentials, true); err != nil {
		t.Fatal(err)
	}
	if added, err := AppendLine(p, "~/.gitconfig"); err != nil || !added {
		t.Fatal(added, err)
	}
	if added, _ := AppendLine(p, "~/.gitconfig"); added {
		t.Fatal("duplicate line added")
	}
	if err := SetCategory(p, Workspace, false); err != nil {
		t.Fatal(err)
	}
	c, _, _ = Load(p, "/h", "/w")
	if !c.Enabled(Credentials) || c.Enabled(Workspace) || !c.Included("/h/.gitconfig") || !c.Denied[Workspace] {
		t.Fatalf("after edits: %v %v", c.Categories, c.Denied)
	}
	if c2 := Parse(Default, "/h", "/w"); !c2.Categories[Credentials] {
		t.Fatal("default must list @credentials")
	}
	if !Parse("", "/h", "/w").Selected("newagent.sessions", "newagent", "sessions") {
		t.Fatal("an agent nobody mentioned must be uploaded")
	}
}

func TestDecideMostSpecificWins(t *testing.T) {
	c := Parse("@codex\n!@claude.sessions\n@claude\n@sessions\n!@comate\n", "/h", "/w")
	for _, tc := range []struct {
		keys []string
		on   bool
	}{
		{[]string{"codex.sessions", "codex", "sessions"}, true},
		{[]string{"codex.memory", "codex", "memory"}, true},
		{[]string{"claude.sessions", "claude", "sessions"}, false},
		{[]string{"claude.memory", "claude", "memory"}, true},
		{[]string{"comate.sessions", "comate", "sessions"}, false},
		{[]string{"other.sessions", "other", "sessions"}, true},
		{[]string{"other.memory", "other", "memory"}, true}, // nobody mentioned it
	} {
		if on := c.Selected(tc.keys...); on != tc.on {
			t.Errorf("%v: %v, want %v", tc.keys, on, tc.on)
		}
	}
}
