// Package adapter knows where each coding agent keeps its sessions and
// long-term memory. Each agent gets its own namespace in the vault (design
// doc 6.2.1): a file's logical path is "<agent>/<path relative to root>".
package adapter

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kind tells how a file is treated by sync.
const (
	KindSession = "session" // conversation logs; expire when unused
	KindMemory  = "memory"  // long-term memory notes; pinned, never expire
)

// SessionFile is one synced file on this machine.
type SessionFile struct {
	Agent   string
	RelPath string // slash-separated, relative to the adapter root
	AbsPath string
	Kind    string
	Thread  *ThreadInfo // Codex: Desktop name and sidebar section of the thread
}

// Logical returns the vault-wide logical path used to derive the session ID.
func (f SessionFile) Logical() string { return f.Agent + "/" + f.RelPath }

// Adapter describes one agent's on-disk layout.
type Adapter interface {
	Name() string
	Root() string
	Sessions() ([]SessionFile, error)
	// NativePath maps a relative path back to this machine, rejecting
	// paths outside the adapter's directories or patterns.
	NativePath(rel string) (string, bool)
}

// matcher classifies a slash path relative to the root ("" = not synced).
type matcher func(rel string) string

type walkAdapter struct {
	name   string
	root   string
	dirs   []string // relative to root
	match  matcher
	enrich func(root string, files []SessionFile) // optional per-agent metadata
}

func (a *walkAdapter) Name() string { return a.name }
func (a *walkAdapter) Root() string { return a.root }

func (a *walkAdapter) Sessions() ([]SessionFile, error) {
	var out []SessionFile
	for _, d := range a.dirs {
		if err := a.walk(filepath.Join(a.root, d), map[string]bool{}, &out); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	if a.enrich != nil {
		a.enrich(a.root, out)
	}
	return out, nil
}

// walk follows symlinked directories (e.g. a memory dir linked to a shared
// disk) with a loop guard; paths are reported under the logical location.
func (a *walkAdapter) walk(dir string, visited map[string]bool, out *[]SessionFile) error {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil // missing or dangling: nothing to sync
	}
	if visited[real] {
		return nil
	}
	visited[real] = true
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		fi, err := os.Stat(p) // follows symlinks
		if err != nil {
			continue
		}
		if fi.IsDir() {
			if !strings.HasPrefix(e.Name(), ".") {
				if err := a.walk(p, visited, out); err != nil {
					return err
				}
			}
			continue
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		r, _ := filepath.Rel(a.root, p)
		rel := filepath.ToSlash(r)
		if kind := a.match(rel); kind != "" {
			*out = append(*out, SessionFile{Agent: a.name, RelPath: rel, AbsPath: p, Kind: kind})
		}
	}
	return nil
}

func (a *walkAdapter) NativePath(rel string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	inDir := false
	for _, d := range a.dirs {
		if strings.HasPrefix(clean, filepath.Clean(d)+string(filepath.Separator)) {
			inDir = true
		}
	}
	if !inDir || a.match(filepath.ToSlash(clean)) == "" {
		return "", false
	}
	return filepath.Join(a.root, clean), true
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// memoryNote matches markdown notes inside a "memory" directory.
func memoryNote(rel string) bool {
	return strings.Contains("/"+rel, "/memory/") && strings.HasSuffix(rel, ".md")
}

// Codex stores rollouts under $CODEX_HOME/sessions/YYYY/MM/DD/*.jsonl. Its
// sqlite state is rebuilt from them by Codex itself, so it is not synced;
// thread names and sections travel in the session metadata instead
// (codexthreads.go).
func Codex() Adapter {
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		root = filepath.Join(home(), ".codex")
	}
	return CodexAt(root)
}

// CodexAt returns the Codex adapter for an explicit $CODEX_HOME.
func CodexAt(root string) Adapter {
	cache := &threadCache{}
	return &walkAdapter{name: "codex", root: root, dirs: []string{"sessions", "archived_sessions"},
		enrich: func(root string, files []SessionFile) {
			threads := cache.get(root)
			for i := range files {
				if info, ok := threads[CodexThreadID(files[i].RelPath)]; ok {
					files[i].Thread = &info
				}
			}
		},
		match: func(rel string) string {
			if strings.HasSuffix(rel, ".jsonl") {
				return KindSession
			}
			return ""
		}}
}

// Claude Code keeps transcripts in ~/.claude/projects/<encoded cwd>/*.jsonl
// (subagents included) and memory notes in projects/<encoded cwd>/memory/.
func Claude() Adapter {
	root := os.Getenv("CLAUDE_CONFIG_DIR")
	if root == "" {
		root = filepath.Join(home(), ".claude")
	}
	return ClaudeAt(root)
}

// ClaudeAt returns the Claude Code adapter for an explicit config dir.
func ClaudeAt(root string) Adapter {
	return &walkAdapter{name: "claude", root: root, dirs: []string{"projects"},
		match: func(rel string) string {
			switch {
			case memoryNote(rel):
				return KindMemory
			case strings.HasSuffix(rel, ".jsonl"):
				return KindSession
			}
			return ""
		}}
}

// Comate keeps chat sessions in ~/.comate-engine/store/chat_session_<id>
// (JSON, rewritten in place) and memory notes in
// ~/.comate/projects/<slug>/memory/. The adapter root is the home directory.
func Comate() Adapter {
	if h := os.Getenv("HEARTH_COMATE_HOME"); h != "" {
		return ComateAt(h)
	}
	return ComateAt(home())
}

// ComateAt returns the Comate adapter rooted at an explicit home directory.
func ComateAt(homeDir string) Adapter {
	return &walkAdapter{name: "comate", root: homeDir, dirs: []string{".comate-engine/store", ".comate/projects"},
		match: func(rel string) string {
			switch {
			case strings.HasPrefix(rel, ".comate-engine/store/chat_session_") &&
				!strings.Contains(strings.TrimPrefix(rel, ".comate-engine/store/"), "/"):
				return KindSession
			case strings.HasPrefix(rel, ".comate/projects/") && memoryNote(rel):
				return KindMemory
			}
			return ""
		}}
}

// All returns every built-in adapter.
func All() []Adapter { return []Adapter{Codex(), Claude(), Comate()} }

// ByName finds an adapter by name.
func ByName(name string) (Adapter, bool) {
	for _, a := range All() {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// KindWorkspaceFile marks personal files kept in the user's workspace.
// They are pinned like memory notes.
const KindWorkspaceFile = "memory"

// dynamicAdapter syncs paths chosen at call time: the workspace and the
// list of workspace-relative files and directories (both may change while
// the daemon runs, e.g. after `hearth learn` or a library pull).
type dynamicAdapter struct {
	name  string
	paths func() (root string, rel []string)
}

// WorkspaceFiles returns the adapter for personal files inside the
// workspace; paths reports the workspace ("" = none) and the chosen paths.
func WorkspaceFiles(paths func() (string, []string)) Adapter {
	return &dynamicAdapter{name: "workspace", paths: paths}
}

const maxWorkspaceFile = 64 << 20

func (a *dynamicAdapter) Name() string { return a.name }
func (a *dynamicAdapter) Root() string { r, _ := a.paths(); return r }

func (a *dynamicAdapter) Sessions() ([]SessionFile, error) {
	root, list := a.paths()
	if root == "" {
		return nil, nil
	}
	var out []SessionFile
	seen := map[string]bool{}
	for _, rel := range list {
		base := filepath.Join(root, filepath.FromSlash(rel))
		filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != base && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			fi, err := os.Stat(p) // follows symlinks
			if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxWorkspaceFile {
				return nil
			}
			r, _ := filepath.Rel(root, p)
			r = filepath.ToSlash(r)
			if !seen[r] {
				seen[r] = true
				out = append(out, SessionFile{Agent: a.name, RelPath: r, AbsPath: p, Kind: KindWorkspaceFile})
			}
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
}

func (a *dynamicAdapter) NativePath(rel string) (string, bool) {
	// Any path inside the workspace: on a new machine the list of chosen
	// paths may not have been pulled yet when sessions are restored.
	root, _ := a.paths()
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if root == "" || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
		clean == ".hearth" || strings.HasPrefix(clean, ".hearth/") || strings.HasPrefix(clean, ".git/") {
		return "", false
	}
	return filepath.Join(root, filepath.FromSlash(clean)), true
}

// KindSelected marks files the user selected in the backup configuration
// (pinned like memory notes: they never expire).
const KindSelected = "memory"

// selectedAdapter syncs the files the user's backup selection lists. Paths
// are stored as "home/<rel>" or "workspace/<rel>" so they restore under the
// next machine's home or workspace; files elsewhere are not synced.
type selectedAdapter struct {
	sel func() (home, workspace string, roots []string, include func(abs string) bool)
}

// SelectedFiles returns the adapter for user-selected backup files.
func SelectedFiles(sel func() (home, workspace string, roots []string, include func(abs string) bool)) Adapter {
	return &selectedAdapter{sel: sel}
}

func (a *selectedAdapter) Name() string { return "files" }
func (a *selectedAdapter) Root() string { h, _, _, _ := a.sel(); return h }

func underDir(p, dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	r, err := filepath.Rel(dir, p)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", false
	}
	return filepath.ToSlash(r), true
}

// relFor names an absolute path: the workspace wins over home (it is usually
// inside home but moves between machines on its own).
func relFor(p, home, ws string) (string, bool) {
	if r, ok := underDir(p, ws); ok {
		return "workspace/" + r, true
	}
	if r, ok := underDir(p, home); ok {
		return "home/" + r, true
	}
	return "", false
}

func (a *selectedAdapter) Sessions() ([]SessionFile, error) {
	home, ws, roots, include := a.sel()
	var out []SessionFile
	seen := map[string]bool{}
	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", ".hearth", "node_modules":
					if p != root {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !include(p) {
				return nil
			}
			fi, err := os.Stat(p)
			if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxWorkspaceFile {
				return nil
			}
			rel, ok := relFor(p, home, ws)
			if !ok || seen[rel] {
				return nil
			}
			seen[rel] = true
			out = append(out, SessionFile{Agent: "files", RelPath: rel, AbsPath: p, Kind: KindSelected})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
}

func (a *selectedAdapter) NativePath(rel string) (string, bool) {
	home, ws, _, _ := a.sel()
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	base, rest, ok := strings.Cut(clean, "/")
	if !ok || rest == "" || rest == "." || rest == ".." || strings.HasPrefix(rest, "../") || filepath.IsAbs(rest) {
		return "", false
	}
	var dir string
	switch base {
	case "home":
		dir = home
	case "workspace":
		dir = ws
	}
	if dir == "" || rest == ".hearth" || strings.HasPrefix(rest, ".hearth/") {
		return "", false
	}
	return filepath.Join(dir, filepath.FromSlash(rest)), true
}
