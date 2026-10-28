package client

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
)

// Usage from this machine is uploaded and ranked across the account.
func TestServerSkillUsage(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	now := time.Unix(1_800_000_000, 0).Unix()
	var b []byte
	for i, ev := range []struct {
		skill string
		n     int
	}{{"bench", 3}, {"lint", 2}, {"rare", 1}} {
		for j := 0; j < ev.n; j++ {
			b = append(b, []byte(`{"time":`+usageItoa(now-int64(i))+`,"event":"get_skill","skill":"`+ev.skill+`"}`+"\n")...)
		}
	}
	if err := os.WriteFile(filepath.Join(a.Home, "usage.jsonl"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"bench", "lint"} { // "rare" was deleted
		addSkill(t, a.Home, s)
	}
	ranks, err := a.ServerUsage(30, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranks) != 2 || ranks[0].Name != "bench" || ranks[1].Name != "lint" || ranks[0].Count != 3 {
		t.Fatalf("deleted skill uploaded or wrong ranking: %+v", ranks)
	}
	// Another account cannot see them.
	c := en.user("other@example.com", "c")
	if r, err := c.ServerUsage(30, 10); err != nil || len(r) != 0 {
		t.Fatalf("other account: %+v %v", r, err)
	}
}

func addSkill(t *testing.T, home, name string) {
	t.Helper()
	d := filepath.Join(LibraryDir(home), "skills", name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: test\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Counts of skills that no longer exist (uploaded by older clients) are
// listed as stale and pruned; existing skills keep theirs.
func TestPruneStaleUsage(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	addSkill(t, a.Home, "keep")
	day := en.clock.now().UTC().Format(time.DateOnly) // the server ranks by its own clock
	if _, err := a.API.PutUserUsage([]api.UsageCount{{Type: "skill", Name: "keep", Day: day, Count: 4},
		{Type: "skill", Name: "old-test", Day: day, Count: 9}, {Type: "skill", Name: "hearth", Day: day, Count: 2}}); err != nil {
		t.Fatal(err)
	}
	stale, err := a.StaleUsage()
	if err != nil || len(stale) != 1 || stale[0].Name != "old-test" {
		t.Fatalf("stale = %+v %v", stale, err)
	}
	if n, err := a.API.PruneUserUsage([]string{"old-test"}); err != nil || n != 1 {
		t.Fatalf("prune = %d %v", n, err)
	}
	ranks, err := a.API.UserUsage(30, 10)
	if err != nil || len(ranks) != 2 || ranks[0].Name != "keep" {
		t.Fatalf("after prune: %+v %v", ranks, err)
	}
	// Another account's prune cannot touch these.
	c := en.user("other@example.com", "c")
	if n, err := c.API.PruneUserUsage([]string{"keep"}); err != nil || n != 0 {
		t.Fatalf("other account pruned %d (%v)", n, err)
	}
}

func usageItoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
