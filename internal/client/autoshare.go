package client

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Automatic sharing: the user's most used skills and agents (account-wide
// ranking) are published to every team where the user may publish
// (maintainer or owner), and republished when they change. config.toml
// [teams] share_top (default 5, 0 = off) and share_exclude control it.

const defaultShareTop = 5

// ShareTopN is the configured number of skills (and of agents) to share.
func ShareTopN(home string) (int, map[string]bool) {
	n, ex := defaultShareTop, map[string]bool{}
	if c, err := UserConfig(home); err == nil && c.Teams != nil {
		if c.Teams.ShareTop != nil {
			n = *c.Teams.ShareTop
		}
		for _, x := range c.Teams.ShareExclude {
			ex[x] = true
		}
	}
	return n, ex
}

// AutoShared is one item handled by ShareTop.
type AutoShared struct {
	Team, Type, Name string
	Op               string // shared | updated | unchanged | blocked (secret scan) | failed
	Version          int64
	Detail           string
}

// ShareTop publishes the top skills and agents to the user's teams. With
// dryRun nothing is uploaded; Op says what would happen.
func (e *Engine) ShareTop(dryRun bool) ([]AutoShared, error) {
	n, exclude := ShareTopN(e.Home)
	if n <= 0 {
		return nil, nil
	}
	teams, err := e.API.Teams()
	if err != nil {
		return nil, err
	}
	var targets []string
	for _, t := range teams {
		if t.Role == "maintainer" || t.Role == "owner" {
			targets = append(targets, t.Name)
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}
	if _, err := e.UploadUsage(); err != nil {
		return nil, err
	}
	lib := library.Library{Root: LibraryDir(e.Home)}
	inLib := func(typ, name string) bool {
		p := filepath.Join(lib.SkillsDir(), name, "SKILL.md")
		if typ == "agent" {
			p = filepath.Join(lib.AgentsDir(), name+".md")
		}
		_, err := os.Stat(p)
		return err == nil
	}
	type item struct{ typ, name string }
	var items []item
	for _, typ := range []string{"skill", "agent"} {
		ranks, err := e.API.UserUsageOf(typ, 30, n+20)
		if err != nil {
			return nil, err
		}
		k := 0
		for _, r := range ranks {
			if k == n {
				break
			}
			// Only the user's own items: linked team items already are shared.
			if exclude[r.Name] || r.Name == "hearth" || !inLib(typ, r.Name) {
				continue
			}
			items = append(items, item{typ, r.Name})
			k++
		}
	}
	var out []AutoShared
	for _, team := range targets {
		pkgs, err := e.API.Packages(team)
		if err != nil {
			out = append(out, AutoShared{Team: team, Op: "failed", Detail: err.Error()})
			continue
		}
		latest := map[string]string{}
		for _, p := range pkgs {
			latest[p.Type+"/"+p.Name] = p.SHA256
		}
		for _, it := range items {
			res := AutoShared{Team: team, Type: it.typ, Name: it.name}
			data, sum, desc, err := e.bundleItem(it.typ, it.name, false)
			var se *SecretsError
			switch {
			case errors.As(err, &se):
				res.Op, res.Detail = "blocked", "secret scan: "+se.Error()
			case err != nil:
				res.Op, res.Detail = "failed", err.Error()
			case latest[it.typ+"/"+it.name] == sum:
				res.Op = "unchanged"
			case dryRun:
				res.Op = "would-share"
			default:
				r, err := e.API.Publish(team, it.typ, it.name, desc, sum, data)
				if err != nil {
					res.Op, res.Detail = "failed", err.Error()
				} else {
					res.Op, res.Version = "shared", r.Version
					if latest[it.typ+"/"+it.name] != "" {
						res.Op = "updated"
					}
				}
			}
			out = append(out, res)
		}
	}
	return out, nil
}
