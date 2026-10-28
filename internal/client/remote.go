package client

import (
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/search"
)

// SearchableSkills returns linked skills (already downloaded) for the MCP
// skill search; it does not touch the network.
func (e *Engine) SearchableSkills() ([]search.Skill, error) {
	var out []search.Skill
	ex, _ := e.Linked()
	for _, sk := range ex.Skills {
		if s, err := search.LoadSkill(sk.Dir, sk.Name, "linked"); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// SearchTeams asks the server to search every trusted team source in
// parallel (server-side index), bounded by timeout. Skills only.
func (e *Engine) SearchTeams(query string, limit int, timeout time.Duration) []search.Result {
	ss, err := LoadSources(e.Home)
	if err != nil {
		return nil
	}
	var mu sync.Mutex
	var out []search.Result
	var wg sync.WaitGroup
	for _, s := range ss {
		if s.Type != "team" || s.Trust != "trusted" {
			continue
		}
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			hits, err := e.API.SearchTeam(s.Team, query, limit)
			if err != nil {
				return
			}
			mu.Lock()
			for _, h := range hits {
				if h.Type != "skill" {
					continue
				}
				sk := search.NewSkill(h.Name, h.Description, "", s.Name+" (not linked)")
				sk.Ref = s.Name + ":" + h.Name
				out = append(out, search.Result{Skill: sk, Score: h.Score})
			}
			mu.Unlock()
		}(s)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
	mu.Lock()
	defer mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return append([]search.Result(nil), out...)
}

func (a *API) SearchTeam(team, query string, limit int) ([]api.SearchHit, error) {
	var r []api.SearchHit
	_, err := a.do("GET", "/v1/teams/"+url.PathEscape(team)+"/search?q="+url.QueryEscape(query)+"&limit="+strconv.Itoa(limit), nil, nil, nil, &r)
	return r, err
}

// FetchSkill downloads a shared skill found by SearchableSkills.
func (e *Engine) FetchSkill(sk search.Skill) (string, error) {
	c, err := e.ResolvePackage(sk.Ref, "skill")
	if err != nil {
		return "", err
	}
	dir, _, err := e.Fetch(c)
	return dir, err
}

// TeamSkills lists the skills shared in every trusted team source (for the
// MCP list_skills), bounded by timeout; first source wins on a name.
func (e *Engine) TeamSkills(timeout time.Duration) []search.Skill {
	ss, err := LoadSources(e.Home)
	if err != nil {
		return nil
	}
	var teams []Source
	for _, s := range ss {
		if s.Type == "team" && s.Trust == "trusted" {
			teams = append(teams, s)
		}
	}
	lists := make([][]search.Skill, len(teams))
	var wg sync.WaitGroup
	for i, s := range teams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ps, err := e.API.Packages(s.Team)
			if err != nil {
				return
			}
			for _, p := range ps {
				if p.Type == "skill" {
					sk := search.NewSkill(p.Name, p.Description, "", s.Name+" (not linked)")
					sk.Ref = s.Name + ":" + p.Name
					lists[i] = append(lists[i], sk)
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		return nil // partial lists would be raced on; the next call retries
	}
	var out []search.Skill
	seen := map[string]bool{}
	for _, l := range lists {
		for _, sk := range l {
			if !seen[sk.Name] {
				seen[sk.Name] = true
				out = append(out, sk)
			}
		}
	}
	return out
}
