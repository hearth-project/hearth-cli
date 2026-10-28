package client

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Link pins a shared skill or agent. Links live in the library
// (library/links.json), so they follow the user to every machine.
type Link struct {
	Type    string `json:"type"` // skill | agent
	Name    string `json:"name"`
	Source  string `json:"source"`
	Version int64  `json:"version,omitempty"` // team packages
	Commit  string `json:"commit,omitempty"`  // git sources
	SHA256  string `json:"sha256"`
	// Auto: linked because it is one of the team's most used items
	// ([teams] use_top); updated and removed by SyncTeamTop.
	Auto bool `json:"auto,omitempty"`
}

type linksFile struct {
	Links []Link `json:"links"`
}

func linksPath(home string) string { return filepath.Join(LibraryDir(home), "links.json") }

// LoadLinks reads library/links.json.
func LoadLinks(home string) ([]Link, error) {
	var f linksFile
	err := loadJSON(linksPath(home), &f)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return f.Links, err
}

func saveLinks(home string, ls []Link) error {
	sort.Slice(ls, func(i, j int) bool { return ls[i].Type+ls[i].Name < ls[j].Type+ls[j].Name })
	// Plain 0644 file inside the library so it is bundled and synced.
	b, err := jsonIndent(linksFile{Links: ls})
	if err != nil {
		return err
	}
	return writeFileAtomic(linksPath(home), b, 0o644)
}

// Candidate is a package found in a source.
type Candidate struct {
	Source      Source
	Type        string
	Name        string
	Description string
	Version     int64
	Commit      string
	SHA256      string
	gitDir      string // git: package directory inside the checkout
}

// GitTTL is how long a git source checkout is reused before fetching again.
var GitTTL = 10 * time.Minute

func (c Candidate) Ref() string { return c.Source.Name + ":" + c.Name }

func (e *Engine) source(name string) (Source, error) {
	ss, err := LoadSources(e.Home)
	if err != nil {
		return Source{}, err
	}
	for _, s := range ss {
		if s.Name == name {
			return s, nil
		}
	}
	return Source{}, fmt.Errorf("no source named %q (see `hearth lib source list`)", name)
}

func (e *Engine) gitDir(s Source) string { return filepath.Join(e.Home, "cache", "git", s.Name) }

// Index lists the packages a source offers. Git sources are fetched first.
func (e *Engine) Index(s Source) ([]Candidate, error) {
	switch s.Type {
	case "team":
		ps, err := e.API.Packages(s.Team)
		if err != nil {
			return nil, fmt.Errorf("source %s: %w", s.Name, err)
		}
		out := make([]Candidate, 0, len(ps))
		for _, p := range ps {
			out = append(out, Candidate{Source: s, Type: p.Type, Name: p.Name, Description: p.Description, Version: p.Version, SHA256: p.SHA256})
		}
		return out, nil
	case "git":
		dir := e.gitDir(s)
		stamp := filepath.Join(dir, ".git", "hearth-fetched")
		var commit string
		// Fetching is the slow part; reuse a checkout younger than GitTTL.
		if fi, err := os.Stat(stamp); err == nil && !e.Refresh && time.Since(fi.ModTime()) < GitTTL {
			out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
			if err == nil {
				commit = strings.TrimSpace(string(out))
			}
		}
		if commit == "" {
			c, err := gitCheckout(s, dir, "")
			if err != nil {
				return nil, fmt.Errorf("source %s: %w", s.Name, err)
			}
			commit = c
			os.WriteFile(stamp, nil, 0o600)
		}
		return scanGit(s, filepath.Join(dir, filepath.FromSlash(s.Subdir)), commit)
	}
	return nil, fmt.Errorf("unknown source type %q", s.Type)
}

// scanGit finds skills/<name>/SKILL.md and agents/<name>.md in a checkout.
// A repository that is itself a flat list of skill directories also works.
func scanGit(s Source, root, commit string) ([]Candidate, error) {
	lib := library.Library{Root: root}
	c, err := lib.Load()
	if err != nil {
		return nil, err
	}
	skills := c.Skills
	if len(skills) == 0 {
		// A repository that is itself a flat list of skill directories.
		if skills, err = library.LoadSkillsDir(root); err != nil {
			return nil, err
		}
	}
	var out []Candidate
	for _, sk := range skills {
		out = append(out, Candidate{Source: s, Type: "skill", Name: sk.Name, Description: skillDescription(sk.Dir), Commit: commit, gitDir: sk.Dir})
	}
	for _, a := range c.Agents {
		out = append(out, Candidate{Source: s, Type: "agent", Name: a.Name, Description: a.Description, Commit: commit,
			gitDir: filepath.Join(lib.AgentsDir(), a.Name+".md")})
	}
	return out, nil
}

// ResolvePackage finds ref ("name" or "source:name") of type typ: the first source
// that has it wins. Items already in the local library are refused.
func (e *Engine) ResolvePackage(ref, typ string) (Candidate, error) {
	srcName, name, scoped := strings.Cut(ref, ":")
	if !scoped {
		name, srcName = ref, ""
	}
	if !library.ValidName(name) {
		return Candidate{}, fmt.Errorf("invalid name %q", name)
	}
	if !scoped && e.inLibrary(typ, name) {
		return Candidate{}, fmt.Errorf("%s %q is already in your local library (local wins); use source:name to link a shared copy under another setup", typ, name)
	}
	ss, err := LoadSources(e.Home)
	if err != nil {
		return Candidate{}, err
	}
	var errs []error
	for _, s := range ss {
		if scoped && s.Name != srcName {
			continue
		}
		cands, err := e.Index(s)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, c := range cands {
			if c.Type == typ && c.Name == name {
				return c, nil
			}
		}
	}
	if scoped && len(errs) == 0 {
		if _, err := e.source(srcName); err != nil {
			return Candidate{}, err
		}
	}
	return Candidate{}, errors.Join(append([]error{fmt.Errorf("%s %q not found in any source", typ, ref)}, errs...)...)
}

func (e *Engine) inLibrary(typ, name string) bool {
	lib := library.Library{Root: LibraryDir(e.Home)}
	p := filepath.Join(lib.SkillsDir(), name, "SKILL.md")
	if typ == "agent" {
		p = filepath.Join(lib.AgentsDir(), name+".md")
	}
	_, err := os.Stat(p)
	return err == nil
}

func (e *Engine) cacheDir(source, typ, name, sum string) string {
	return filepath.Join(e.Home, "cache", "pkgs", source, typ, name, sum[:16])
}

// Fetch downloads a candidate into the content cache and returns its
// directory and content hash. Cached entries are reused.
func (e *Engine) Fetch(c Candidate) (string, string, error) {
	tmp := filepath.Join(e.Home, "cache", "tmp-"+fmt.Sprint(time.Now().UnixNano()))
	if err := os.MkdirAll(filepath.Dir(tmp), 0o700); err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)
	var sum string
	switch c.Source.Type {
	case "team":
		if c.SHA256 != "" {
			if d := e.cacheDir(c.Source.Name, c.Type, c.Name, c.SHA256); exists(d) {
				return d, c.SHA256, nil
			}
		}
		data, _, s, err := e.API.GetPackage(c.Source.Team, c.Type, c.Name, c.Version)
		if err != nil {
			return "", "", err
		}
		got, err := library.Extract(data, tmp)
		if err != nil {
			return "", "", err
		}
		if got != s || (c.SHA256 != "" && got != c.SHA256) {
			return "", "", fmt.Errorf("package %s: content hash mismatch", c.Ref())
		}
		sum = got
	case "git":
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return "", "", err
		}
		var err error
		if c.Type == "agent" {
			b, rerr := os.ReadFile(c.gitDir)
			if rerr != nil {
				return "", "", rerr
			}
			err = os.WriteFile(filepath.Join(tmp, "AGENT.md"), b, 0o644)
		} else {
			_, err = library.CopyTree(c.gitDir, tmp)
		}
		if err != nil {
			return "", "", err
		}
		if _, sum, err = (library.Library{Root: tmp}).Bundle(); err != nil {
			return "", "", err
		}
	default:
		return "", "", fmt.Errorf("unknown source type %q", c.Source.Type)
	}
	dst := e.cacheDir(c.Source.Name, c.Type, c.Name, sum)
	if exists(dst) {
		return dst, sum, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", "", err
	}
	return dst, sum, os.Rename(tmp, dst)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func skillDescription(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return ""
	}
	return library.SkillDescription(b)
}
