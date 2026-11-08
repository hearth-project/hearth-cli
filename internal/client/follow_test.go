package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Linked skills follow the newest version a trusted team publishes, unless
// they are pinned; every move is recorded.
func TestFollowUpdates(t *testing.T) {
	en := newEnv(t)
	alice := en.machine("alice")
	bob := en.user("bob@example.com", "bob")
	bobCodex, _ := bob.libTargets(t)
	if err := alice.API.CreateTeam("infer"); err != nil {
		t.Fatal(err)
	}
	if err := alice.API.SetMember("infer", "bob@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	skill := func(name, body string) {
		libWrite(t, alice, "skills/"+name+"/SKILL.md", "---\nname: "+name+"\ndescription: "+name+"\n---\n"+body+"\n")
		if _, _, err := alice.Publish("infer", "skill", name, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"follows", "frozen", "oldpick"} {
		skill(n, "v1")
	}
	if err := AddSource(bob.Home, Source{Name: "team", Type: "team", Team: "infer"}); err != nil {
		t.Fatal(err)
	}
	link := func(name string, version int64, pinned bool) {
		t.Helper()
		c, err := bob.ResolvePackage("team:"+name, "skill")
		if err != nil {
			t.Fatal(err)
		}
		if version > 0 {
			c.Version, c.SHA256 = version, ""
		}
		_, sum, err := bob.Fetch(c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bob.AddLinkWith(c, sum, pinned); err != nil {
			t.Fatal(err)
		}
	}
	link("follows", 0, false)
	link("frozen", 0, true) // --pin
	skill("follows", "v2")
	skill("frozen", "v2")
	skill("oldpick", "v2")
	link("oldpick", 1, true) // --version 1: going back to an older version

	version := func(name string) int64 {
		ls, _ := LoadLinks(bob.Home)
		for _, l := range ls {
			if l.Name == name {
				return l.Version
			}
		}
		return -1
	}
	if version("oldpick") != 1 {
		t.Fatalf("link --version 1 landed on v%d", version("oldpick"))
	}

	ups, err := bob.FollowUpdates()
	if err != nil || len(ups) != 1 || ups[0].Name != "follows" || ups[0].From != 1 || ups[0].To != 2 || ups[0].Auto {
		t.Fatalf("updates: %+v %v", ups, err)
	}
	if got := strings.Join(ups[0].Changes, ","); got != "~ SKILL.md" {
		t.Fatalf("changes: %q", got)
	}
	if version("follows") != 2 || version("frozen") != 1 || version("oldpick") != 1 {
		t.Fatalf("versions: follows v%d, frozen v%d, oldpick v%d", version("follows"), version("frozen"), version("oldpick"))
	}
	if _, err := bob.Render(false); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(bobCodex, "skills", name, "SKILL.md"))
		return string(b)
	}
	if !strings.Contains(read("follows"), "v2") || !strings.Contains(read("frozen"), "v1") || !strings.Contains(read("oldpick"), "v1") {
		t.Fatal("the agents do not have the versions the links say")
	}
	if rec := RecentLinkUpdates(bob.Home, time.Unix(0, 0)); len(rec) != 1 || rec[0].Name != "follows" {
		t.Fatalf("recorded: %+v", rec)
	}
	if again, err := bob.FollowUpdates(); err != nil || len(again) != 0 {
		t.Fatalf("nothing is newer now: %+v %v", again, err)
	}

	// [teams] follow_updates = false stops it; turning it on again catches up.
	off := false
	cfg, _ := UserConfig(bob.Home)
	cfg.Teams = &library.TeamsConfig{FollowUpdates: &off}
	if err := SaveUserConfig(bob.Home, cfg); err != nil {
		t.Fatal(err)
	}
	skill("follows", "v3")
	if ups, err := bob.FollowUpdates(); err != nil || len(ups) != 0 || version("follows") != 2 {
		t.Fatalf("follow_updates = false: %+v %v, v%d", ups, err, version("follows"))
	}
	cfg.Teams = nil
	if err := SaveUserConfig(bob.Home, cfg); err != nil {
		t.Fatal(err)
	}
	if ups, err := bob.FollowUpdates(); err != nil || len(ups) != 1 || version("follows") != 3 {
		t.Fatalf("catching up: %+v %v, v%d", ups, err, version("follows"))
	}

	// A link from an untrusted source or a git commit never follows by itself;
	// a team that cannot be reached leaves its links where they are.
	ls, _ := LoadLinks(bob.Home)
	ls = append(ls, Link{Type: "skill", Name: "fromgit", Source: "somegit", Commit: "abc123", SHA256: "x"})
	if err := saveLinks(bob.Home, ls); err != nil {
		t.Fatal(err)
	}
	if ups, err := bob.FollowUpdates(); err != nil || len(ups) != 0 {
		t.Fatalf("git link: %+v %v", ups, err)
	}
}
