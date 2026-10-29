package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Manifest records every path hearth rendered, so later syncs can update
// or remove exactly those paths and detect manual edits.
type Manifest struct {
	Entries map[string]Entry `json:"entries"`
}

// Entry is one rendered path.
type Entry struct {
	Kind   string `json:"kind"`             // "link" | "file"
	Link   string `json:"link,omitempty"`   // symlink target
	SHA256 string `json:"sha256,omitempty"` // file content hash
	Item   string `json:"item"`             // e.g. "skill:benchmark"
}

func loadManifest(path string) (*Manifest, error) {
	m := &Manifest{Entries: map[string]Entry{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if m.Entries == nil {
		m.Entries = map[string]Entry{}
	}
	return m, nil
}

func (m *Manifest) save(path string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Options controls Render.
type Options struct {
	Manifest  string // path of the manifest file
	BackupDir string // where adopted originals are moved
	Adopt     bool   // replace unmanaged existing paths (after backing them up)
	DryRun    bool
	Extra     Extra // linked shared items; library items with the same name win
	// Present returns where an agent already loads a skill of this name
	// outside the target (e.g. the project's .codex/skills), or "". Such
	// skills are not linked again: one copy per agent is enough.
	Present func(t Target, skill string) string
}

// Extra holds skills and agents that come from outside the library.
type Extra struct {
	Skills []Skill
	Agents []*Agent
}

// Action is one line of a render report.
type Action struct {
	Path   string
	Item   string
	Op     string // create, update, unchanged, remove, adopt, skip, warn
	Detail string
}

type want struct {
	Entry
	content []byte
}

func hashOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// current reports whether path currently matches entry e.
func current(path string, e Entry) (exists, matches bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return false, false
	}
	if e.Kind == "link" {
		dst, err := os.Readlink(path)
		return true, err == nil && fi.Mode()&os.ModeSymlink != 0 && dst == e.Link
	}
	if !fi.Mode().IsRegular() {
		return true, false
	}
	b, err := os.ReadFile(path)
	return true, err == nil && hashOf(b) == e.SHA256
}

// Render makes every target reflect the library and returns what it did.
func Render(lib Library, targets []Target, opt Options) ([]Action, error) {
	c, err := lib.Load()
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, s := range c.Skills {
		have["skill:"+s.Name] = true
	}
	for _, a := range c.Agents {
		have["agent:"+a.Name] = true
	}
	var acts []Action
	// Linked shared skills were chosen explicitly (or are a team's most
	// used): always rendered, whatever the resident list says.
	linked := map[string]bool{}
	for _, s := range opt.Extra.Skills {
		if have["skill:"+s.Name] {
			acts = append(acts, Action{Item: "skill:" + s.Name, Op: "warn", Detail: "local library copy shadows the linked one"})
			continue
		}
		linked[s.Name] = true
		c.Skills = append(c.Skills, s)
	}
	for _, a := range opt.Extra.Agents {
		if have["agent:"+a.Name] {
			acts = append(acts, Action{Item: "agent:" + a.Name, Op: "warn", Detail: "local library copy shadows the linked one"})
			continue
		}
		c.Agents = append(c.Agents, a)
	}
	for _, p := range c.Problems {
		acts = append(acts, Action{Op: "warn", Detail: p})
	}
	resident := map[string]bool{}
	for _, n := range c.Resident {
		resident[n] = true
	}
	wants := map[string]want{}
	for _, t := range targets {
		for _, s := range c.Skills {
			if c.Resident != nil && !resident[s.Name] && !linked[s.Name] {
				continue // on demand only, via `hearth mcp`
			}
			if opt.Present != nil {
				if at := opt.Present(t, s.Name); at != "" && at != t.skillPath(s.Name) {
					acts = append(acts, Action{Path: at, Item: "skill:" + s.Name, Op: "present"})
					continue
				}
			}
			wants[t.skillPath(s.Name)] = want{Entry: Entry{Kind: "link", Link: s.Dir, Item: "skill:" + s.Name}}
		}
		if t.SkillsOnly() {
			continue
		}
		for _, a := range c.Agents {
			b, lost, err := t.renderAgent(a)
			if err != nil {
				acts = append(acts, Action{Item: "agent:" + a.Name, Op: "warn", Detail: fmt.Sprintf("%s: %v", t.Name, err)})
				continue
			}
			p := t.agentPath(a.Name)
			wants[p] = want{Entry: Entry{Kind: "file", SHA256: hashOf(b), Item: "agent:" + a.Name}, content: b}
			if len(lost) > 0 {
				acts = append(acts, Action{Path: p, Item: "agent:" + a.Name, Op: "warn", Detail: fmt.Sprintf("%s ignores %v", t.Name, lost)})
			}
		}
		if c.Instructions != "" {
			if b := t.renderInstructions(c.Instructions); b != nil {
				wants[t.instrPath()] = want{Entry: Entry{Kind: "file", SHA256: hashOf(b), Item: "instructions"}, content: b}
			} else {
				wants[t.instrPath()] = want{Entry: Entry{Kind: "link", Link: c.Instructions, Item: "instructions"}}
			}
		}
	}

	m, err := loadManifest(opt.Manifest)
	if err != nil {
		return nil, err
	}
	stamp := time.Now().Format("20060102-150405")
	paths := make([]string, 0, len(wants))
	for p := range wants {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		w := wants[p]
		act := Action{Path: p, Item: w.Item}
		exists, matches := current(p, w.Entry)
		old, managed := m.Entries[p]
		_, oldMatches := current(p, old)
		switch {
		case matches:
			act.Op = "unchanged"
		case !exists:
			act.Op = "create"
		case managed && oldMatches:
			act.Op = "update"
		case managed:
			act.Op, act.Detail = "skip", "modified outside hearth; run `hearth lib import` or delete it"
		case w.Kind == "link" && otherHearthLink(p, w.Link):
			// Linked by another Hearth home on this machine (~/.hearth
			// before a workspace took over): this library is the one now.
			act.Op, act.Detail = "update", "was linked to another Hearth home's library"
		case !opt.Adopt && strings.HasPrefix(w.Item, "skill:") && isSkillDir(p):
			// The agent already has its own copy of this skill.
			act.Op = "present"
		case opt.Adopt:
			act.Op = "adopt"
			act.Detail = filepath.Join(opt.BackupDir, stamp, strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", "__"))
		default:
			act.Op, act.Detail = "skip", "exists and is not managed by hearth (use --adopt to back it up and replace)"
		}
		acts = append(acts, act)
		if opt.DryRun || act.Op == "skip" || act.Op == "present" {
			continue
		}
		if act.Op != "unchanged" {
			if err := apply(p, w, act); err != nil {
				return acts, fmt.Errorf("%s: %w", p, err)
			}
		}
		m.Entries[p] = w.Entry
	}
	// Remove paths we created earlier that the library no longer contains.
	var stale []string
	for p := range m.Entries {
		if _, ok := wants[p]; !ok {
			stale = append(stale, p)
		}
	}
	sort.Strings(stale)
	for _, p := range stale {
		e := m.Entries[p]
		exists, matches := current(p, e)
		act := Action{Path: p, Item: e.Item, Op: "remove"}
		if exists && !matches {
			act.Op, act.Detail = "skip", "no longer in library but modified outside hearth; left in place"
		}
		acts = append(acts, act)
		if opt.DryRun {
			continue
		}
		if act.Op == "remove" && exists {
			if err := os.Remove(p); err != nil {
				return acts, err
			}
		}
		delete(m.Entries, p)
	}
	if opt.DryRun {
		return acts, nil
	}
	return acts, m.save(opt.Manifest)
}

func apply(p string, w want, act Action) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	switch act.Op {
	case "adopt":
		if err := os.MkdirAll(filepath.Dir(act.Detail), 0o700); err != nil {
			return err
		}
		if err := os.Rename(p, act.Detail); err != nil {
			return err
		}
	case "update":
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	if w.Kind == "link" {
		return os.Symlink(w.Link, p)
	}
	tmp := p + ".hearth-tmp"
	if err := os.WriteFile(tmp, w.content, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// isSkillDir reports whether p (or what it links to) holds a SKILL.md.
func isSkillDir(p string) bool {
	_, err := os.Stat(filepath.Join(p, "SKILL.md"))
	return err == nil
}

// otherHearthLink reports whether path is a symlink into a Hearth library
// other than the one want points into (".../.hearth/library/...").
func otherHearthLink(path, want string) bool {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	dst, err := os.Readlink(path)
	if err != nil || dst == want {
		return false
	}
	const mark = string(filepath.Separator) + ".hearth" + string(filepath.Separator) + "library" + string(filepath.Separator)
	i, j := strings.Index(dst, mark), strings.Index(want, mark)
	return i >= 0 && j >= 0 && dst[:i] != want[:j]
}
