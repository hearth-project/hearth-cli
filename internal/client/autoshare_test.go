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

// The most used skills and agents are shared with teams where the user can
// publish, republished when changed, and never for built-in agent roles.
func TestShareTop(t *testing.T) {
	en := newEnv(t)
	en.clock.add(time.Until(en.clock.now())*-1 + time.Second) // rankings use the server's clock: today
	alice := en.machine("alice")
	alice.libTargets(t)
	if err := alice.API.CreateTeam("infer"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	var usage []string
	for i, n := range []int{9, 8, 7, 6, 5, 4} { // s0 most used ... s5 least
		name := "s" + string(rune('0'+i))
		libWrite(t, alice, "skills/"+name+"/SKILL.md", "---\nname: "+name+"\ndescription: skill "+name+"\n---\nsteps\n")
		for j := 0; j < n; j++ {
			b, _ := json.Marshal(map[string]any{"time": now, "event": "get_skill", "skill": name})
			usage = append(usage, string(b))
		}
	}
	if err := os.WriteFile(filepath.Join(alice.Home, "usage.jsonl"), []byte(strings.Join(usage, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	libWrite(t, alice, "agents/rev.md", "---\nname: rev\ndescription: reviews\n---\nReview.\n")
	// A Codex session that started the rev agent twice and a built-in role.
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	var lines []string
	for _, a := range []string{"rev", "rev", "explorer"} {
		args, _ := json.Marshal(map[string]string{"task_name": "x", "message": "go", "agent_type": a})
		b, _ := json.Marshal(map[string]any{"timestamp": ts, "type": "response_item",
			"payload": map[string]any{"type": "function_call", "name": "spawn_agent", "arguments": string(args)}})
		lines = append(lines, string(b))
	}
	p := filepath.Join(alice.codex, "sessions/2026/10/27/rollout-2026-10-27T10-00-00-0199dddd-0000-7000-8000-000000000001.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := alice.ShareTop(false)
	if err != nil {
		t.Fatal(err)
	}
	shared := map[string]string{}
	for _, r := range res {
		shared[r.Type+"/"+r.Name] = r.Op
	}
	for _, want := range []string{"skill/s0", "skill/s1", "skill/s2", "skill/s3", "skill/s4", "agent/rev"} {
		if shared[want] != "shared" {
			t.Fatalf("%s not shared: %+v", want, res)
		}
	}
	if _, ok := shared["skill/s5"]; ok {
		t.Fatalf("6th skill shared: %+v", res)
	}
	if _, ok := shared["agent/explorer"]; ok {
		t.Fatal("built-in role shared")
	}
	if ag, err := alice.API.UserUsageOf("agent", 30, 10); err != nil || len(ag) != 1 || ag[0].Name != "rev" || ag[0].Count != 2 {
		t.Fatalf("agent usage %+v %v", ag, err)
	}
	// Unchanged items are not uploaded again; a changed one gets a version.
	libWrite(t, alice, "skills/s1/SKILL.md", "---\nname: s1\ndescription: skill s1\n---\nnew steps\n")
	res, _ = alice.ShareTop(false)
	for _, r := range res {
		want := "unchanged"
		if r.Name == "s1" {
			want = "updated"
		}
		if r.Op != want {
			t.Fatalf("%s: %s, want %s", r.Name, r.Op, want)
		}
	}
	// Members cannot publish: nothing for them; share_top = 0 turns it off.
	bob := en.user("bob@example.com", "bob")
	if err := alice.API.SetMember("infer", "bob@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	libWrite(t, bob, "skills/b/SKILL.md", "---\nname: b\ndescription: b\n---\nx\n")
	if res, err := bob.ShareTop(false); err != nil || len(res) != 0 {
		t.Fatalf("member shared: %+v %v", res, err)
	}
	cfg, _ := UserConfig(alice.Home)
	zero := 0
	cfg.Teams = &library.TeamsConfig{ShareTop: &zero}
	if err := SaveUserConfig(alice.Home, cfg); err != nil {
		t.Fatal(err)
	}
	if res, err := alice.ShareTop(false); err != nil || len(res) != 0 {
		t.Fatalf("share_top = 0 still shared: %+v %v", res, err)
	}
}

// Two machines keep one shared latest copy: an unchanged stale copy is
// replaced by the team's version, while a genuine new edit is published
// and becomes the newest version.
func TestShareTopAdoptsTeamVersion(t *testing.T) {
	en := newEnv(t)
	en.clock.add(time.Until(en.clock.now())*-1 + time.Second)
	alice := en.machine("alice")
	alice.libTargets(t)
	if err := alice.API.CreateTeam("infer"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	var usage []string
	for i := 0; i < 5; i++ {
		b, _ := json.Marshal(map[string]any{"time": now, "event": "get_skill", "skill": "s"})
		usage = append(usage, string(b))
	}
	if err := os.WriteFile(filepath.Join(alice.Home, "usage.jsonl"), []byte(strings.Join(usage, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	libWrite(t, alice, "skills/s/SKILL.md", "---\nname: s\ndescription: s\n---\nfrom alice\n")
	if _, err := alice.LibPush(false); err != nil {
		t.Fatal(err)
	}
	alice2 := en.machine("alice2")
	alice2.libTargets(t)
	if _, err := alice2.LibPull(false); err != nil {
		t.Fatal(err)
	}
	res, err := alice.ShareTop(false)
	if err != nil || len(res) != 1 || res[0].Op != "shared" {
		t.Fatalf("alice first share: %+v %v", res, err)
	}

	res, err = alice2.ShareTop(false)
	if err != nil || len(res) != 1 || res[0].Op != "unchanged" {
		t.Fatalf("alice2 did not adopt the initial version: %+v %v", res, err)
	}
	skill := func(m *machine) string {
		b, _ := os.ReadFile(filepath.Join(LibraryDir(m.Home), "skills/s/SKILL.md"))
		return string(b)
	}
	if !strings.Contains(skill(alice2), "from alice\n") {
		t.Fatalf("alice2 kept the stale copy: %q", skill(alice2))
	}
	pkgs, err := alice.API.Packages("infer")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].Version != 1 {
		t.Fatalf("refresh created a version: %+v", pkgs)
	}

	// A real edit on alice becomes v2; alice2's unchanged old copy is
	// replaced automatically.
	libWrite(t, alice, "skills/s/SKILL.md", "---\nname: s\ndescription: s\n---\nfrom alice v2\n")
	res, err = alice.ShareTop(false)
	if err != nil || len(res) != 1 || res[0].Op != "updated" {
		t.Fatalf("alice edit did not publish: %+v %v", res, err)
	}
	res, err = alice2.ShareTop(false)
	if err != nil || len(res) != 1 || res[0].Op != "refreshed" || res[0].Version != 2 {
		t.Fatalf("alice2 did not adopt v2: %+v %v", res, err)
	}
	if !strings.Contains(skill(alice2), "from alice v2\n") {
		t.Fatalf("alice2 kept the old copy: %q", skill(alice2))
	}
	// A real edit on alice2 becomes v3, and alice adopts it.
	libWrite(t, alice2, "skills/s/SKILL.md", "---\nname: s\ndescription: s\n---\nfrom alice2 v3\n")
	res, err = alice2.ShareTop(false)
	if err != nil || len(res) != 1 || res[0].Op != "updated" {
		t.Fatalf("alice2 edit did not publish: %+v %v", res, err)
	}
	res, err = alice.ShareTop(false)
	if err != nil || len(res) != 1 || res[0].Op != "refreshed" || res[0].Version != 3 {
		t.Fatalf("alice did not adopt v3: %+v %v", res, err)
	}
	if !strings.Contains(skill(alice), "from alice2 v3\n") {
		t.Fatalf("alice kept the old copy: %q", skill(alice))
	}
	// Removing the package from the team must not make the next auto-share
	// recreate it from this machine.
	if _, err := alice.API.Unpublish("infer", "skill", "s"); err != nil {
		t.Fatal(err)
	}
	res, err = alice.ShareTop(false)
	if err != nil || len(res) != 1 || res[0].Op != "removed" {
		t.Fatalf("removed package was shared again: %+v %v", res, err)
	}
	if pkgs, err = alice.API.Packages("infer"); err != nil || len(pkgs) != 0 {
		t.Fatalf("removed package came back: %+v %v", pkgs, err)
	}
}

// The heartbeat tells a connected daemon when a team package changed, so
// it can sync immediately instead of waiting for the 20-minute library
// poll.
func TestHeartbeatTeamRevision(t *testing.T) {
	en := newEnv(t)
	alice := en.machine("alice")
	alice.libTargets(t)
	if err := alice.API.CreateTeam("infer"); err != nil {
		t.Fatal(err)
	}
	before, err := alice.API.HeartbeatState()
	if err != nil {
		t.Fatal(err)
	}
	libWrite(t, alice, "skills/s/SKILL.md", "---\nname: s\ndescription: s\n---\nv1\n")
	if _, _, err := alice.Publish("infer", "skill", "s", false); err != nil {
		t.Fatal(err)
	}
	after, err := alice.API.HeartbeatState()
	if err != nil {
		t.Fatal(err)
	}
	if before.TeamRevision == "" || after.TeamRevision == "" || before.TeamRevision == after.TeamRevision {
		t.Fatalf("team revision did not change: %q -> %q", before.TeamRevision, after.TeamRevision)
	}
	again, err := alice.API.HeartbeatState()
	if err != nil {
		t.Fatal(err)
	}
	if again.TeamRevision != after.TeamRevision {
		t.Fatalf("unchanged team got a new revision: %q -> %q", after.TeamRevision, again.TeamRevision)
	}
}
