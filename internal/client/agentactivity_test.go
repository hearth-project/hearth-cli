package client

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
)

func conv(agent string, at time.Time) RemoteSession {
	return RemoteSession{Session: api.Session{ActiveAt: at.Unix(), Status: "active"}, Meta: SessionMeta{Agent: agent, Kind: "session"}}
}

func TestRecentAgentsAndMachineSelection(t *testing.T) {
	isolateAgentHomes(t)
	home := t.TempDir()
	os.MkdirAll(LibraryDir(home), 0o755)
	os.WriteFile(filepath.Join(LibraryDir(home), "agents.json"), []byte(`{"enabled":["codex","claude","comate"]}`), 0o644)
	now := time.Unix(1_800_000_000, 0)
	use := AgentUse([]RemoteSession{
		conv("codex", now), conv("codex", now.Add(-time.Hour)),
		conv("comate", now.Add(-3*24*time.Hour)),
		conv("claude", now.Add(-40*24*time.Hour)),
	})
	recent, stale := RecentAgents(home, use)
	if !reflect.DeepEqual(recent, []string{"codex", "comate"}) || len(stale) != 1 || stale[0].Agent != "claude" {
		t.Fatalf("recent %v stale %+v", recent, stale)
	}
	// No conversations yet: every enabled agent.
	if r, _ := RecentAgents(home, nil); len(r) != 3 {
		t.Fatalf("new account: %v", r)
	}
	if HasMachineAgents(home) || len(EnabledLibTargets(home)) != 3 {
		t.Fatal("without a selection every enabled agent is rendered")
	}
	if err := SetMachineAgents(home, recent); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tg := range EnabledLibTargets(home) {
		names = append(names, tg.Name)
	}
	if !reflect.DeepEqual(names, []string{"codex", "comate"}) {
		t.Fatalf("targets %v", names)
	}
	AddMachineAgent(home, "claude")
	if len(MachineAgents(home)) != 3 {
		t.Fatalf("pull claude: %v", MachineAgents(home))
	}
}
