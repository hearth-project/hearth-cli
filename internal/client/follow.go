package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Skills and agents linked from a trusted team follow the team's newest
// version: the daemon moves the link when a new version is published and
// records what changed, so the user can see it (hearth status, hearth lib
// links). A link made with --pin / --version stays put, and so does one from
// a git source (its "newest" is whatever the branch holds; hearth lib update
// shows it and --yes applies it).

// FollowEnabled reports [teams] follow_updates (default true).
func FollowEnabled(home string) bool {
	if c, err := UserConfig(home); err == nil && c.Teams != nil && c.Teams.FollowUpdates != nil {
		return *c.Teams.FollowUpdates
	}
	return true
}

// LinkUpdate is one version change of a linked item on this machine.
type LinkUpdate struct {
	Time    int64    `json:"time"`
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Source  string   `json:"source"`
	From    int64    `json:"from"`
	To      int64    `json:"to"`
	Auto    bool     `json:"auto,omitempty"`    // one of the team's most used items (use_top)
	Changes []string `json:"changes,omitempty"` // "+ path", "- path", "~ path" (first few)
}

const (
	maxLinkUpdates = 50
	maxChangesKept = 10
)

func linkUpdatesPath(home string) string { return filepath.Join(home, "link-updates.json") }

// RecentLinkUpdates returns the changes recorded since the given time,
// newest first.
func RecentLinkUpdates(home string, since time.Time) []LinkUpdate {
	var all []LinkUpdate
	b, err := os.ReadFile(linkUpdatesPath(home))
	if err != nil || json.Unmarshal(b, &all) != nil {
		return nil
	}
	var out []LinkUpdate
	for _, u := range all {
		if time.Unix(u.Time, 0).After(since) {
			out = append(out, u)
		}
	}
	return out
}

func recordLinkUpdates(home string, ups []LinkUpdate) {
	if len(ups) == 0 {
		return
	}
	var all []LinkUpdate
	if b, err := os.ReadFile(linkUpdatesPath(home)); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	for i := range ups {
		if len(ups[i].Changes) > maxChangesKept {
			ups[i].Changes = ups[i].Changes[:maxChangesKept]
		}
	}
	all = append(append([]LinkUpdate{}, ups...), all...)
	if len(all) > maxLinkUpdates {
		all = all[:maxLinkUpdates]
	}
	if b, err := json.MarshalIndent(all, "", "  "); err == nil {
		_ = writeFileAtomic(linkUpdatesPath(home), b, 0o600)
	}
}

// changesSince lists what differs between a cached older copy of an item and
// the new directory ("" or a missing old copy: everything is new).
func (e *Engine) changesSince(l Link, newDir string) []string {
	old := e.cacheDir(l.Source, l.Type, l.Name, l.SHA256)
	if !exists(old) {
		old = ""
	}
	return diffTrees(old, newDir)
}

// FollowUpdates moves every following link (not pinned, not automatic, from a
// trusted team source) to the newest version its team published, and returns
// what changed. A source that cannot be reached keeps its links where they
// are; the error says which. Rendering the new content into the agents is
// the caller's job (Render).
func (e *Engine) FollowUpdates() ([]LinkUpdate, error) {
	if !FollowEnabled(e.Home) {
		return nil, nil
	}
	ls, err := LoadLinks(e.Home)
	if err != nil {
		return nil, err
	}
	index := map[string]map[string]Candidate{} // source -> type/name -> newest
	var errs []error
	var ups []LinkUpdate
	for i, l := range ls {
		if l.Auto || l.Pinned || l.Commit != "" {
			continue // automatic links are SyncTeamTop's; the others are the user's choice
		}
		s, err := e.source(l.Source)
		if err != nil || s.Type != "team" || s.Trust != "trusted" {
			continue
		}
		m, seen := index[s.Name]
		if !seen {
			cands, err := e.Index(s)
			if err != nil {
				errs = append(errs, err)
			} else {
				m = map[string]Candidate{}
				for _, c := range cands {
					m[c.Type+"/"+c.Name] = c
				}
			}
			index[s.Name] = m
		}
		c, ok := m[l.Type+"/"+l.Name]
		if !ok || c.SHA256 == "" || c.SHA256 == l.SHA256 {
			continue
		}
		dir, sum, err := e.Fetch(c)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if sum == l.SHA256 {
			continue
		}
		ups = append(ups, LinkUpdate{Time: time.Now().Unix(), Type: l.Type, Name: l.Name, Source: l.Source,
			From: l.Version, To: c.Version, Changes: e.changesSince(l, dir)})
		ls[i].Version, ls[i].SHA256 = c.Version, sum
	}
	if len(ups) > 0 {
		if err := saveLinks(e.Home, ls); err != nil {
			return nil, err
		}
		sort.SliceStable(ups, func(i, j int) bool { return ups[i].Name < ups[j].Name })
		recordLinkUpdates(e.Home, ups)
	}
	return ups, errors.Join(errs...)
}
