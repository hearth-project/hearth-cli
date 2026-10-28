package client

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
)

func TestTeamUsageRanking(t *testing.T) {
	en := newEnv(t)
	alice := en.machine("alice")
	bob := en.user("bob@example.com", "bob")
	alice.API.CreateTeam("t1")
	alice.API.SetMember("t1", "bob@example.com", "member")
	for _, n := range []string{"bench", "profiler"} {
		libWrite(t, alice, "skills/"+n+"/SKILL.md", "---\nname: "+n+"\n---\nx\n")
		alice.Publish("t1", "skill", n, false)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	codex := t.TempDir()
	bob.Adapters = []adapter.Adapter{adapter.CodexAt(codex)}
	f := filepath.Join(codex, "sessions/2026/10/22/rollout-1.jsonl")
	os.MkdirAll(filepath.Dir(f), 0o700)
	os.WriteFile(f, []byte(
		`{"timestamp":"`+now+`","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"skills: (file: skills/bench/SKILL.md) (file: skills/profiler/SKILL.md)"}]}}
{"timestamp":"`+now+`","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat /root/.codex/skills/bench/SKILL.md\"}"}}
{"timestamp":"`+now+`","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"sed -n 1,80p skills/bench/SKILL.md\"}"}}
{"timestamp":"`+now+`","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/private-thing/SKILL.md\"}"}}
`), 0o600)
	// bob also loaded profiler once through the MCP server.
	os.WriteFile(filepath.Join(bob.Home, "usage.jsonl"), []byte(`{"time":`+itoa(time.Now().Unix())+`,"event":"get_skill","skill":"profiler"}`+"\n"), 0o600)

	u, _ := bob.LocalUsage(time.Now().AddDate(0, 0, -1))
	day := time.Now().UTC().Format(time.DateOnly)
	if u[day]["bench"] != 2 || u[day]["profiler"] != 1 || u[day]["private-thing"] != 1 {
		t.Fatalf("local usage (system prompt must not count): %v", u)
	}
	en.clock.mu.Lock()
	en.clock.t = time.Now()
	en.clock.mu.Unlock()
	n, err := bob.ShareUsage("t1")
	if err != nil || n != 2 {
		t.Fatalf("share: %d %v", n, err)
	}
	if n, _ := bob.ShareUsage("t1"); n != 2 {
		t.Fatal("re-sharing must be idempotent")
	}
	rs, err := alice.API.TeamUsage("t1", 30)
	if err != nil || len(rs) != 2 || rs[0].Name != "bench" || rs[0].Count != 2 || rs[0].Users != 1 {
		t.Fatalf("ranking: %+v %v", rs, err)
	}
	for _, r := range rs {
		if r.Name == "private-thing" {
			t.Fatal("private skill name left the machine")
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
