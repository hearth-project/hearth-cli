package client

import (
	"sort"
	"time"
)

// RecentWindow: an agent counts as in use when the user worked with it
// within this window before their latest conversation with any agent.
const RecentWindow = 14 * 24 * time.Hour

// AgentActivity is when the user last wrote a conversation with an agent.
type AgentActivity struct {
	Agent string
	Last  time.Time
	Count int
}

// AgentUse summarizes conversations per agent, most recently used first.
func AgentUse(all []RemoteSession) []AgentActivity {
	by := map[string]*AgentActivity{}
	for _, rs := range all {
		if rs.MetaErr != nil || rs.Meta.Agent == "" || !IsConversation(rs) || rs.Status == "expired" {
			continue
		}
		a := by[rs.Meta.Agent]
		if a == nil {
			a = &AgentActivity{Agent: rs.Meta.Agent}
			by[rs.Meta.Agent] = a
		}
		a.Count++
		at := rs.ActiveAt
		if at == 0 {
			at = rs.LastAccessedAt
		}
		if t := time.Unix(at, 0); t.After(a.Last) {
			a.Last = t
		}
	}
	out := make([]AgentActivity, 0, len(by))
	for _, a := range by {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// RecentAgents picks the enabled agents a new machine restores: those used
// within RecentWindow of the latest conversation. With no conversations
// yet (a new account) every enabled agent is restored.
func RecentAgents(home string, use []AgentActivity) (recent []string, stale []AgentActivity) {
	on := enabledSet(home)
	var used []AgentActivity
	for _, a := range use {
		if on[a.Agent] {
			used = append(used, a)
		}
	}
	if len(used) == 0 {
		return EnabledAgents(home), nil
	}
	newest := used[0].Last
	for _, a := range used {
		if newest.Sub(a.Last) <= RecentWindow {
			recent = append(recent, a.Agent)
		} else {
			stale = append(stale, a)
		}
	}
	return recent, stale
}
