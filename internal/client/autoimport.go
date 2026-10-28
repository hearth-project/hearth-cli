package client

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// Setup copies the skills and agents a machine already has in its agents
// (~/.codex, ~/.claude, ~/.comate and the workspace's project-level ones)
// into the library, so the next machine gets them without `hearth learn`.
// Each name is imported once per machine (lib-imported.json): an item the
// user later deletes from the library is not brought back from here.
// Instructions files are left to `hearth lib import`.

// AutoImported is the outcome of AutoImport.
type AutoImported struct {
	Imported []string // "skill:name" / "agent:name"
	Blocked  []string // "skill:name (file:line: rule)": looks like it holds a credential
}

type importedState struct {
	Seen []string `json:"seen"`
}

func importedPath(home string) string { return filepath.Join(home, "lib-imported.json") }

// AutoImportTargets are the agent directories AutoImport reads.
func AutoImportTargets(home string) []library.Target {
	var out []library.Target
	seen := map[string]bool{}
	add := func(t library.Target) {
		if fi, err := os.Stat(t.Root); err != nil || !fi.IsDir() || seen[t.Root] {
			return
		}
		seen[t.Root] = true
		out = append(out, t.WithoutInstructions())
	}
	for _, t := range library.DefaultTargets() {
		add(t)
	}
	if ws := paths.Workspace(); ws != "" {
		if h, _ := os.UserHomeDir(); ws != h {
			add(library.CodexTarget(filepath.Join(ws, ".codex")))
			add(library.ClaudeTarget(filepath.Join(ws, ".claude")))
			add(library.ComateTarget(filepath.Join(ws, ".comate")))
		}
	}
	return out
}

// AutoImport imports the items not in the library yet and not imported
// from this machine before. Items that look like they contain a credential
// are removed again and reported (the library is not end-to-end encrypted).
func (e *Engine) AutoImport() (AutoImported, error) {
	var r AutoImported
	var st importedState
	if err := loadJSON(importedPath(e.Home), &st); err != nil && !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	seen := map[string]bool{}
	for _, s := range st.Seen {
		seen[s] = true
	}
	lib := library.Library{Root: LibraryDir(e.Home)}
	have := map[string]bool{}
	if c, err := lib.Load(); err == nil {
		for _, s := range c.Skills {
			have["skill:"+s.Name] = true
		}
		for _, a := range c.Agents {
			have["agent:"+a.Name] = true
		}
	}
	for _, t := range AutoImportTargets(e.Home) {
		acts, err := library.Import(lib, t, RenderManifest(e.Home), false)
		if err != nil {
			return r, err
		}
		for _, a := range acts {
			if a.Op != "import" {
				continue
			}
			dst := filepath.Join(lib.SkillsDir(), strings.TrimPrefix(a.Item, "skill:"))
			if strings.HasPrefix(a.Item, "agent:") {
				dst = filepath.Join(lib.AgentsDir(), strings.TrimPrefix(a.Item, "agent:")+".md")
			}
			if seen[a.Item] && !have[a.Item] {
				// Imported from here before and deleted from the library since.
				os.RemoveAll(dst)
				continue
			}
			if fs, _ := library.ScanSecrets(dst); len(fs) > 0 {
				os.RemoveAll(dst)
				f := fs[0]
				if rel, err := filepath.Rel(dst, f.File); err == nil && rel != "." {
					f.File = rel
				}
				r.Blocked = append(r.Blocked, a.Item+" ("+f.String()+")")
				continue
			}
			have[a.Item] = true // marked seen below
			r.Imported = append(r.Imported, a.Item)
		}
	}
	// Remember what is in the library now too: deleting one of those later
	// must not bring it back from this machine's agent directories.
	for k := range have {
		seen[k] = true
	}
	st.Seen = st.Seen[:0]
	for k := range seen {
		st.Seen = append(st.Seen, k)
	}
	sort.Strings(st.Seen)
	sort.Strings(r.Imported)
	return r, saveJSON(importedPath(e.Home), st)
}
