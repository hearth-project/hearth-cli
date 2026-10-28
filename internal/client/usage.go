package client

import (
	"bufio"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/library"
)

// Skill usage comes from two places (design doc 9.6): get_skill calls of
// the MCP server (usage.jsonl) and skill loads recorded in local session
// logs (Codex reading skills/<name>/SKILL.md through a tool call, Claude
// Code's Skill tool).

var skillPathRE = regexp.MustCompile(`skills/([a-z0-9][a-z0-9._-]{0,63})/SKILL\.md`)

// UsageByDay maps day (YYYY-MM-DD, UTC) -> skill -> count.
type UsageByDay map[string]map[string]int64

func (u UsageByDay) add(t time.Time, skill string) {
	d := t.UTC().Format(time.DateOnly)
	if u[d] == nil {
		u[d] = map[string]int64{}
	}
	u[d][skill]++
}

// LocalUsage collects skill usage on this machine since `since`.
func (e *Engine) LocalUsage(since time.Time) (UsageByDay, error) {
	u, _, err := e.LocalUsageAll(since)
	return u, err
}

// LocalUsageAll collects skill and agent usage (subagents started by type:
// Codex spawn_agent agent_type, Claude Code Task subagent_type).
func (e *Engine) LocalUsageAll(since time.Time) (UsageByDay, UsageByDay, error) {
	u, ag := UsageByDay{}, UsageByDay{}
	if f, err := os.Open(filepath.Join(e.Home, "usage.jsonl")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var ev struct {
				Time  int64  `json:"time"`
				Skill string `json:"skill"`
			}
			if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Skill != "" && ev.Time >= since.Unix() {
				u.add(time.Unix(ev.Time, 0), ev.Skill)
			}
		}
		f.Close()
	}
	for _, a := range e.Adapters {
		if a.Name() != "codex" && a.Name() != "claude" {
			continue
		}
		files, err := a.Sessions()
		if err != nil {
			continue
		}
		for _, sf := range files {
			if fi, err := os.Stat(sf.AbsPath); err != nil || fi.ModTime().Before(since) {
				continue
			}
			scanSessionUsage(a.Name(), sf.AbsPath, since, u, ag)
		}
	}
	return u, ag, nil
}

func scanSessionUsage(agent, path string, since time.Time, u, ag UsageByDay) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			Timestamp string          `json:"timestamp"`
			Payload   json.RawMessage `json:"payload"`
			Message   json.RawMessage `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, line.Timestamp)
		if err != nil || ts.Before(since) {
			continue
		}
		switch agent {
		case "codex": // only tool calls count, not the skill list in the system prompt
			var p struct {
				Type      string `json:"type"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if json.Unmarshal(line.Payload, &p) == nil && p.Type == "function_call" {
				for _, m := range skillPathRE.FindAllStringSubmatch(p.Arguments, -1) {
					u.add(ts, m[1])
				}
				if strings.HasSuffix(p.Name, "spawn_agent") {
					var a struct {
						AgentType string `json:"agent_type"`
					}
					if json.Unmarshal([]byte(p.Arguments), &a) == nil && a.AgentType != "" {
						ag.add(ts, a.AgentType)
					}
				}
			}
		case "claude":
			var m struct {
				Content []struct {
					Type  string `json:"type"`
					Name  string `json:"name"`
					Input struct {
						Skill        string `json:"skill"`
						SubagentType string `json:"subagent_type"`
					} `json:"input"`
				} `json:"content"`
			}
			if json.Unmarshal(line.Message, &m) == nil {
				for _, c := range m.Content {
					if c.Type == "tool_use" && c.Name == "Skill" && c.Input.Skill != "" {
						u.add(ts, c.Input.Skill)
					}
					if c.Type == "tool_use" && (c.Name == "Task" || c.Name == "Agent") && c.Input.SubagentType != "" {
						ag.add(ts, c.Input.SubagentType)
					}
				}
			}
		}
	}
}

func (a *API) PutUsage(team string, counts []api.UsageCount) (int, error) {
	var r struct {
		Accepted int `json:"accepted"`
	}
	_, err := a.do("PUT", "/v1/teams/"+url.PathEscape(team)+"/usage", counts, nil, nil, &r)
	return r.Accepted, err
}

func (a *API) PutUserUsage(counts []api.UsageCount) (int, error) {
	var r struct {
		Accepted int `json:"accepted"`
	}
	_, err := a.do("PUT", "/v1/usage", counts, nil, nil, &r)
	return r.Accepted, err
}

func (a *API) UserUsage(days, limit int) ([]api.UsageRank, error) {
	return a.UserUsageOf("skill", days, limit)
}

// UserUsageOf ranks the account's skills or agents (typ).
func (a *API) UserUsageOf(typ string, days, limit int) ([]api.UsageRank, error) {
	var r []api.UsageRank
	_, err := a.do("GET", "/v1/usage?type="+typ+"&days="+strconv.Itoa(days)+"&limit="+strconv.Itoa(limit), nil, nil, nil, &r)
	return r, err
}

// UploadUsage sends this machine's skill counts for the last 30 days to the
// user's own account, so the ranking covers all their machines.
func (e *Engine) UploadUsage() (int, error) {
	u, ag, err := e.LocalUsageAll(time.Now().AddDate(0, 0, -30))
	if err != nil {
		return 0, err
	}
	known, knownAgents := e.KnownSkills(), e.KnownAgents()
	var counts []api.UsageCount
	for day, m := range u {
		for skill, n := range m {
			if known[skill] { // old transcripts still mention deleted skills
				counts = append(counts, api.UsageCount{Type: "skill", Name: skill, Day: day, Count: n})
			}
		}
	}
	for day, m := range ag {
		for name, n := range m {
			if knownAgents[name] { // built-in roles (explorer, worker, ...) are not the user's
				counts = append(counts, api.UsageCount{Type: "agent", Name: name, Day: day, Count: n})
			}
		}
	}
	if len(counts) == 0 {
		return 0, nil
	}
	return e.API.PutUserUsage(counts)
}

// ServerUsage uploads this machine's counts first, then returns the
// account-wide ranking.
func (e *Engine) ServerUsage(days, limit int) ([]api.UsageRank, error) {
	if _, err := e.UploadUsage(); err != nil {
		return nil, err
	}
	return e.API.UserUsage(days, limit)
}

func (a *API) TeamUsage(team string, days int) ([]api.UsageRank, error) {
	var r []api.UsageRank
	_, err := a.do("GET", "/v1/teams/"+url.PathEscape(team)+"/usage?days="+strconv.Itoa(days), nil, nil, nil, &r)
	return r, err
}

// ShareUsage uploads the last 30 days of counts for skills published in
// team. Nothing about private skills leaves the machine.
func (e *Engine) ShareUsage(team string) (int, error) {
	pkgs, err := e.API.Packages(team)
	if err != nil {
		return 0, err
	}
	published := map[string]bool{}
	for _, p := range pkgs {
		if p.Type == "skill" {
			published[p.Name] = true
		}
	}
	u, err := e.LocalUsage(time.Now().AddDate(0, 0, -30))
	if err != nil {
		return 0, err
	}
	var counts []api.UsageCount
	for day, m := range u {
		for skill, n := range m {
			if published[skill] {
				counts = append(counts, api.UsageCount{Type: "skill", Name: skill, Day: day, Count: n})
			}
		}
	}
	if len(counts) == 0 {
		return 0, nil
	}
	return e.API.PutUsage(team, counts)
}

// AutoShareTeams lists teams the daemon shares usage with (config.toml
// [usage] share_with, else the legacy file).
func AutoShareTeams(home string) []string {
	if c, err := UserConfig(home); err == nil && usageSet(&c) {
		return c.Usage.ShareWith
	}
	var s struct {
		ShareWith []string `json:"share_usage_with"`
	}
	loadJSON(filepath.Join(LibraryDir(home), "usage-sharing.json"), &s)
	return s.ShareWith
}

// SetAutoShare enables or disables automatic sharing with a team.
func SetAutoShare(home, team string, on bool) error {
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	if c.Usage == nil {
		c.Usage = &library.UsageConfig{}
	}
	out := c.Usage.ShareWith[:0]
	for _, t := range c.Usage.ShareWith {
		if t != team {
			out = append(out, t)
		}
	}
	c.Usage.ShareWith = out
	if on {
		c.Usage.ShareWith = append(c.Usage.ShareWith, team)
	}
	if err := SaveUserConfig(home, c); err != nil {
		return err
	}
	os.Remove(filepath.Join(LibraryDir(home), "usage-sharing.json"))
	return nil
}

// KnownSkills are the skills that exist for this user: the library, linked
// shared skills, the built-in hearth skill and every skill installed in an
// agent on this machine (system skills, workspace skills). Usage of
// anything else (deleted, renamed, test skills) is not uploaded.
func (e *Engine) KnownSkills() map[string]bool {
	known := map[string]bool{"hearth": true}
	if c, err := (library.Library{Root: LibraryDir(e.Home)}).Load(); err == nil {
		for _, s := range c.Skills {
			known[s.Name] = true
		}
	}
	if ls, err := LoadLinks(e.Home); err == nil {
		for _, l := range ls {
			if l.Type == "skill" {
				known[l.Name] = true
			}
		}
	}
	// Team skills loaded on demand through the skill search (cache/pkgs/
	// <source>/skill/<name>): their use is what ranks them in the team.
	if srcs, err := os.ReadDir(filepath.Join(e.Home, "cache", "pkgs")); err == nil {
		for _, src := range srcs {
			if ents, err := os.ReadDir(filepath.Join(e.Home, "cache", "pkgs", src.Name(), "skill")); err == nil {
				for _, en := range ents {
					known[en.Name()] = true
				}
			}
		}
	}
	var dirs []string
	for _, t := range EnabledLibTargets(e.Home) {
		dirs = append(dirs, filepath.Join(t.Root, "skills"), filepath.Join(t.Root, "skills", ".system"))
	}
	for _, ds := range AgentSkillDirs(e.Home) {
		dirs = append(dirs, ds...)
	}
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, en := range ents {
			if _, err := os.Stat(filepath.Join(d, en.Name(), "SKILL.md")); err == nil {
				known[en.Name()] = true
			}
		}
	}
	return known
}

func (a *API) PruneUserUsage(names []string) (int64, error) {
	var r struct {
		Deleted int64 `json:"deleted"`
	}
	_, err := a.do("POST", "/v1/usage/prune", api.UsagePruneRequest{Names: names}, nil, nil, &r)
	return r.Deleted, err
}

// StaleUsage lists the skills the account has usage counts for on the
// server (last 365 days) that no longer exist here, most used first.
func (e *Engine) StaleUsage() ([]api.UsageRank, error) {
	ranks, err := e.API.UserUsage(365, 1000)
	if err != nil {
		return nil, err
	}
	known := e.KnownSkills()
	var out []api.UsageRank
	for _, r := range ranks {
		if !known[r.Name] {
			out = append(out, r)
		}
	}
	return out, nil
}

// KnownAgents are the agents this user has: the library, linked shared
// agents, and agent files installed in an agent on this machine.
func (e *Engine) KnownAgents() map[string]bool {
	known := map[string]bool{}
	if c, err := (library.Library{Root: LibraryDir(e.Home)}).Load(); err == nil {
		for _, a := range c.Agents {
			known[a.Name] = true
		}
	}
	if ls, err := LoadLinks(e.Home); err == nil {
		for _, l := range ls {
			if l.Type == "agent" {
				known[l.Name] = true
			}
		}
	}
	for _, t := range EnabledLibTargets(e.Home) {
		ents, _ := os.ReadDir(filepath.Join(t.Root, "agents"))
		for _, en := range ents {
			n := strings.TrimSuffix(strings.TrimSuffix(en.Name(), ".toml"), ".md")
			if n != en.Name() {
				known[n] = true
			}
		}
	}
	return known
}
