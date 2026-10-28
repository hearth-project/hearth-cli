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
