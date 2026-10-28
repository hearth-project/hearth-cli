package client

import (
	"fmt"
	"slices"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Team items in use: each team's most used skills and agents (ranked on the
// server by its members' use) are linked automatically, so they are in
// every agent's own skill and agent lists and get invoked like the user's
// own. Less used ones stay reachable through the skill-search MCP.
// config.toml [teams] use_top (default 20, 0 = off) and use_exclude.

const defaultUseTop = 20

// UseTopN is the configured number of team skills (and of agents) to keep.
func UseTopN(home string) (int, map[string]bool) {
	n, ex := defaultUseTop, map[string]bool{}
	if c, err := UserConfig(home); err == nil && c.Teams != nil {
		if c.Teams.UseTop != nil {
			n = *c.Teams.UseTop
		}
		for _, x := range c.Teams.UseExclude {
			ex[x] = true
		}
	}
	return n, ex
}

func addUseExclude(home, name string) error {
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	if c.Teams == nil {
		c.Teams = &library.TeamsConfig{}
	}
	if slices.Contains(c.Teams.UseExclude, name) {
		return nil
	}
	c.Teams.UseExclude = append(c.Teams.UseExclude, name)
	return SaveUserConfig(home, c)
}

// TeamUsed is one item handled by SyncTeamTop.
type TeamUsed struct {
	Source, Type, Name string
	Op                 string // linked | updated | removed | unchanged | failed
	Version            int64
	Uses               int64
	Detail             string
}

// SyncTeamTop links the hottest items of every trusted team source and
// drops automatic links that fell out of the top. Items the user has in
// the library or linked by hand win and are skipped. A team that cannot be
// reached keeps its current links. Reports whether links.json changed.
func (e *Engine) SyncTeamTop(dryRun bool) ([]TeamUsed, bool, error) {
	n, exclude := UseTopN(e.Home)
	ss, err := LoadSources(e.Home)
	if err != nil {
		return nil, false, err
	}
	ls, err := LoadLinks(e.Home)
	if err != nil {
		return nil, false, err
	}
	manual := map[string]bool{}
	oldAuto := map[string]Link{}
	for _, l := range ls {
		if l.Auto {
			oldAuto[l.Type+"/"+l.Name] = l
		} else {
			manual[l.Type+"/"+l.Name] = true
		}
	}
	own := map[string]bool{}
	if c, err := (library.Library{Root: LibraryDir(e.Home)}).Load(); err == nil {
		for _, s := range c.Skills {
			own["skill/"+s.Name] = true
		}
		for _, a := range c.Agents {
			own["agent/"+a.Name] = true
		}
	}
	type pick struct {
		c    Candidate
		uses int64
	}
	want := map[string]pick{}
	unreachable := map[string]bool{}
	var res []TeamUsed
	if n > 0 {
		for _, s := range ss {
			if s.Type != "team" || s.Trust != "trusted" {
				continue
			}
			ps, err := e.API.Packages(s.Team)
			if err != nil {
				unreachable[s.Name] = true
				res = append(res, TeamUsed{Source: s.Name, Op: "failed", Detail: err.Error()})
				continue
			}
			count := map[string]int{}
			for _, p := range ps { // hottest first
				k := p.Type + "/" + p.Name
				if p.Uses <= 0 || count[p.Type] >= n || exclude[p.Name] || own[k] || manual[k] {
					continue
				}
				if _, taken := want[k]; taken { // an earlier team has it
					continue
				}
				count[p.Type]++
				want[k] = pick{Candidate{Source: s, Type: p.Type, Name: p.Name, Description: p.Description, Version: p.Version, SHA256: p.SHA256}, p.Uses}
			}
		}
	}
	changed := false
	out := make([]Link, 0, len(ls))
	have := map[string]bool{}
	for _, l := range ls {
		k := l.Type + "/" + l.Name
		if !l.Auto {
			out = append(out, l)
			continue
		}
		p, ok := want[k]
		switch {
		case !ok && unreachable[l.Source] && !exclude[l.Name] && !own[k]:
			out = append(out, l) // keep until the team answers again
			have[k] = true
		case !ok:
			changed = true
			res = append(res, TeamUsed{Source: l.Source, Type: l.Type, Name: l.Name, Op: "removed", Version: l.Version})
		case p.c.SHA256 == l.SHA256 && p.c.Source.Name == l.Source:
			out = append(out, l)
			have[k] = true
			res = append(res, TeamUsed{Source: l.Source, Type: l.Type, Name: l.Name, Op: "unchanged", Version: l.Version, Uses: p.uses})
		}
		// Otherwise a newer version (or another team ranks it higher): below.
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		if !have[k] {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		p := want[k]
		old, wasAuto := oldAuto[k]
		r := TeamUsed{Source: p.c.Source.Name, Type: p.c.Type, Name: p.c.Name, Op: "linked", Version: p.c.Version, Uses: p.uses}
		if wasAuto {
			r.Op = "updated"
		}
		if dryRun {
			res = append(res, r)
			continue
		}
		_, sum, err := e.Fetch(p.c)
		if err != nil {
			r.Op, r.Detail = "failed", err.Error()
			res = append(res, r)
			if wasAuto { // keep the old version rather than lose the item
				out = append(out, old)
			}
			continue
		}
		res = append(res, r)
		changed = true
		out = append(out, Link{Type: p.c.Type, Name: p.c.Name, Source: p.c.Source.Name, Version: p.c.Version, SHA256: sum, Auto: true})
	}
	if dryRun || !changed {
		return res, false, nil
	}
	if err := saveLinks(e.Home, out); err != nil {
		return res, false, fmt.Errorf("save links: %w", err)
	}
	return res, true, nil
}
