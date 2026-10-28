package client

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// AddLink records a fetched candidate in library/links.json.
func (e *Engine) AddLink(c Candidate, sum string) (Link, error) {
	ls, err := LoadLinks(e.Home)
	if err != nil {
		return Link{}, err
	}
	l := Link{Type: c.Type, Name: c.Name, Source: c.Source.Name, Version: c.Version, Commit: c.Commit, SHA256: sum}
	out := ls[:0]
	for _, x := range ls {
		if x.Type != l.Type || x.Name != l.Name {
			out = append(out, x)
		}
	}
	return l, saveLinks(e.Home, append(out, l))
}

// Unlink removes a link; its cache entry is left for other machines' reuse.
// An automatic team link is also added to [teams] use_exclude, so the next
// sync does not bring it back.
func (e *Engine) Unlink(typ, name string) error {
	ls, err := LoadLinks(e.Home)
	if err != nil {
		return err
	}
	out := ls[:0]
	auto := false
	for _, x := range ls {
		if x.Type != typ || x.Name != name {
			out = append(out, x)
		} else if x.Auto {
			auto = true
		}
	}
	if len(out) == len(ls) {
		return fmt.Errorf("%s %q is not linked", typ, name)
	}
	if auto {
		if err := addUseExclude(e.Home, name); err != nil {
			return err
		}
	}
	return saveLinks(e.Home, out)
}

// materialize makes sure a link's exact content is in the cache, fetching
// it from its source if needed (e.g. on a new machine).
func (e *Engine) materialize(l Link) (string, error) {
	d := e.cacheDir(l.Source, l.Type, l.Name, l.SHA256)
	if exists(d) {
		return d, nil
	}
	s, err := e.source(l.Source)
	if err != nil {
		return "", err
	}
	c := Candidate{Source: s, Type: l.Type, Name: l.Name, Version: l.Version, Commit: l.Commit, SHA256: l.SHA256}
	if s.Type == "git" {
		dir := e.gitDir(s)
		if _, err := gitCheckout(s, dir, l.Commit); err != nil {
			return "", err
		}
		cands, err := scanGit(s, filepath.Join(dir, filepath.FromSlash(s.Subdir)), l.Commit)
		if err != nil {
			return "", err
		}
		for _, x := range cands {
			if x.Type == l.Type && x.Name == l.Name {
				c.gitDir = x.gitDir
			}
		}
		if c.gitDir == "" {
			return "", fmt.Errorf("%s %s not found at commit %s", l.Type, l.Name, l.Commit)
		}
	}
	got, sum, err := e.Fetch(c)
	if err != nil {
		return "", err
	}
	if sum != l.SHA256 {
		return "", fmt.Errorf("%s %s from %s: content changed upstream for the pinned version (hash mismatch)", l.Type, l.Name, l.Source)
	}
	return got, nil
}

// Linked materializes every link and returns them as render extras.
// Failures are reported per link and do not stop the others.
func (e *Engine) Linked() (library.Extra, []string) {
	var ex library.Extra
	var problems []string
	ls, err := LoadLinks(e.Home)
	if err != nil {
		return ex, []string{err.Error()}
	}
	for _, l := range ls {
		d, err := e.materialize(l)
		if err != nil {
			problems = append(problems, fmt.Sprintf("link %s:%s: %v", l.Source, l.Name, err))
			continue
		}
		switch l.Type {
		case "skill":
			ex.Skills = append(ex.Skills, library.Skill{Name: l.Name, Dir: d})
		case "agent":
			b, err := os.ReadFile(filepath.Join(d, "AGENT.md"))
			if err == nil {
				var a *library.Agent
				if a, err = library.ParseAgent(b); err == nil {
					ex.Agents = append(ex.Agents, a)
				}
			}
			if err != nil {
				problems = append(problems, fmt.Sprintf("link %s:%s: %v", l.Source, l.Name, err))
			}
		}
	}
	return ex, problems
}

// Update is a newer version available for a link.
type Update struct {
	Link    Link
	New     Candidate
	NewDir  string
	NewSum  string
	Changes []string // "+ path", "- path", "~ path"
}

// CheckUpdates resolves every link (or only name) against its source.
func (e *Engine) CheckUpdates(name string) ([]Update, error) {
	// Updates must see the latest upstream state, not a cached checkout.
	prev := e.Refresh
	e.Refresh = true
	defer func() { e.Refresh = prev }()
	ls, err := LoadLinks(e.Home)
	if err != nil {
		return nil, err
	}
	var out []Update
	for _, l := range ls {
		if name != "" && l.Name != name {
			continue
		}
		c, err := e.ResolvePackage(l.Source+":"+l.Name, l.Type)
		if err != nil {
			return out, err
		}
		if c.SHA256 != "" && c.SHA256 == l.SHA256 {
			continue
		}
		d, sum, err := e.Fetch(c)
		if err != nil {
			return out, err
		}
		if sum == l.SHA256 {
			continue
		}
		old, _ := e.materialize(l)
		out = append(out, Update{Link: l, New: c, NewDir: d, NewSum: sum, Changes: diffTrees(old, d)})
	}
	return out, nil
}

// diffTrees lists files added, removed or changed between two directories.
func diffTrees(a, b string) []string {
	ha, hb := treeHashes(a), treeHashes(b)
	var out []string
	for p, h := range hb {
		if ha[p] == "" {
			out = append(out, "+ "+p)
		} else if ha[p] != h {
			out = append(out, "~ "+p)
		}
	}
	for p := range ha {
		if hb[p] == "" {
			out = append(out, "- "+p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][2:] < out[j][2:] })
	return out
}

func treeHashes(root string) map[string]string {
	m := map[string]string{}
	if root == "" {
		return m
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		h := sha256.Sum256(b)
		m[filepath.ToSlash(rel)] = hex.EncodeToString(h[:])
		return nil
	})
	return m
}

// Publish uploads a library skill or agent as a new version in team.
func (e *Engine) Publish(team, typ, name string, allowSecrets bool) (int64, bool, error) {
	data, sum, desc, err := e.bundleItem(typ, name, allowSecrets)
	if err != nil {
		return 0, false, err
	}
	r, err := e.API.Publish(team, typ, name, desc, sum, data)
	return r.Version, r.Created, err
}

// bundleItem packs a library skill or agent for publishing (secret scan
// first unless allowSecrets).
func (e *Engine) bundleItem(typ, name string, allowSecrets bool) (data []byte, sum, desc string, err error) {
	lib := library.Library{Root: LibraryDir(e.Home)}
	tmp, err := os.MkdirTemp(e.Home, "publish-")
	if err != nil {
		return nil, "", "", err
	}
	defer os.RemoveAll(tmp)
	var root string
	switch typ {
	case "skill":
		root = filepath.Join(lib.SkillsDir(), name)
		b, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
		if err != nil {
			return nil, "", "", fmt.Errorf("skill %q is not in your library: %w", name, err)
		}
		desc = library.SkillDescription(b)
	case "agent":
		b, err := os.ReadFile(filepath.Join(lib.AgentsDir(), name+".md"))
		if err != nil {
			return nil, "", "", fmt.Errorf("agent %q is not in your library: %w", name, err)
		}
		a, err := library.ParseAgent(b)
		if err != nil {
			return nil, "", "", err
		}
		desc, root = a.Description, tmp
		if err := os.WriteFile(filepath.Join(tmp, "AGENT.md"), b, 0o644); err != nil {
			return nil, "", "", err
		}
	default:
		return nil, "", "", fmt.Errorf("type must be skill or agent")
	}
	if !allowSecrets {
		fs, err := library.ScanSecrets(root)
		if err != nil {
			return nil, "", "", err
		}
		if len(fs) > 0 {
			return nil, "", "", &SecretsError{Findings: fs}
		}
	}
	data, sum, err = library.Library{Root: root}.Bundle()
	return data, sum, desc, err
}
