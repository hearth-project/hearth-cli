package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// A team's most used items are kept in a member's agents automatically
// (even with a resident list), follow new versions, drop out when they are
// no longer used, and stay out once the member unlinks one.
func TestSyncTeamTop(t *testing.T) {
	en := newEnv(t)
	en.clock.add(time.Until(en.clock.now())*-1 + time.Second)
	alice := en.machine("alice")
	alice.libTargets(t)
	if err := alice.API.CreateTeam("infer"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	var usage []string
	for i, n := range []int{3, 2, 1} {
		name := "t" + string(rune('0'+i))
		libWrite(t, alice, "skills/"+name+"/SKILL.md", "---\nname: "+name+"\ndescription: team skill "+name+"\n---\nv1\n")
		for j := 0; j < n; j++ {
			b, _ := json.Marshal(map[string]any{"time": now, "event": "get_skill", "skill": name})
			usage = append(usage, string(b))
		}
	}
	libWrite(t, alice, "skills/unused/SKILL.md", "---\nname: unused\ndescription: nobody uses it\n---\nx\n")
	os.WriteFile(filepath.Join(alice.Home, "usage.jsonl"), []byte(strings.Join(usage, "\n")+"\n"), 0o600)
	if _, err := alice.ShareTop(false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := alice.Publish("infer", "skill", "unused", false); err != nil {
		t.Fatal(err)
	}

	bob := en.user("bob@example.com", "bob")
	bobCodex, _ := bob.libTargets(t)
	if err := alice.API.SetMember("infer", "bob@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bob.SyncTeamSources(); err != nil {
		t.Fatal(err)
	}
	two := 2
	cfg, _ := UserConfig(bob.Home)
	cfg.Teams = &library.TeamsConfig{UseTop: &two}
	cfg.Skills = &library.SkillsConfig{Resident: []string{"mine"}}
	libWrite(t, bob, "skills/mine/SKILL.md", "---\nname: mine\n---\nx\n")
	if err := SaveUserConfig(bob.Home, cfg); err != nil {
		t.Fatal(err)
	}
	ops := func(dry bool) map[string]string {
		t.Helper()
		res, _, err := bob.SyncTeamTop(dry)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for _, r := range res {
			m[r.Name] = r.Op
		}
		return m
	}
	if got := ops(false); got["t0"] != "linked" || got["t1"] != "linked" || got["t2"] != "" || got["unused"] != "" {
		t.Fatalf("first sync %v", got)
	}
	if _, err := bob.Render(false); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"t0", "t1", "mine"} {
		if _, err := os.Stat(filepath.Join(bobCodex, "skills", n, "SKILL.md")); err != nil {
			t.Fatalf("%s not rendered for Codex: %v", n, err)
		}
	}
	if got := ops(false); got["t0"] != "unchanged" {
		t.Fatalf("second sync %v", got)
	}
	// A new version is followed.
	libWrite(t, alice, "skills/t0/SKILL.md", "---\nname: t0\ndescription: team skill t0\n---\nv2\n")
	if _, err := alice.ShareTop(false); err != nil {
		t.Fatal(err)
	}
	if got := ops(false); got["t0"] != "updated" {
		t.Fatalf("after update %v", got)
	}
	bob.Render(false)
	if b, _ := os.ReadFile(filepath.Join(bobCodex, "skills/t0/SKILL.md")); !strings.Contains(string(b), "v2") {
		t.Fatalf("t0 not updated: %s", b)
	}
	// Unlinking an automatic one excludes it; t2 takes its place.
	if err := bob.Unlink("skill", "t1"); err != nil {
		t.Fatal(err)
	}
	if got := ops(false); got["t1"] != "" || got["t2"] != "linked" {
		t.Fatalf("after unlink %v", got)
	}
	if n, ex := UseTopN(bob.Home); n != 2 || !ex["t1"] {
		t.Fatalf("use_exclude not recorded: %d %v", n, ex)
	}
	// Turned off: the automatic links go.
	zero := 0
	cfg, _ = UserConfig(bob.Home)
	cfg.Teams.UseTop = &zero
	SaveUserConfig(bob.Home, cfg)
	if got := ops(false); got["t0"] != "removed" || got["t2"] != "removed" {
		t.Fatalf("off %v", got)
	}
	if ls, _ := LoadLinks(bob.Home); len(ls) != 0 {
		t.Fatalf("links left %+v", ls)
	}
}
