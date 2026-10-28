package client

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// Workspace files: personal agent state that lives inside the user's
// workspace rather than in $HOME (memory notes, project agent settings,
// MCP configs, the memory MCP graph, tool configs). The chosen paths are
// a synced setting; the files themselves sync end-to-end encrypted like
// sessions (adapter "workspace", pinned so they never expire) and are
// restored under the workspace of the next machine, wherever it is.

// WorkspaceFilesPath is the synced list of workspace-relative paths.
func WorkspaceFilesPath(home string) string {
	return filepath.Join(LibraryDir(home), "workspace-files.json")
}

type workspaceFiles struct {
	Paths []string `json:"paths"` // relative to the workspace; files or directories
}

// WorkspaceFileList returns the configured workspace paths.
func WorkspaceFileList(home string) []string {
	var w workspaceFiles
	loadJSON(WorkspaceFilesPath(home), &w)
	return w.Paths
}

// AddWorkspaceFiles adds paths (relative to the workspace) to the list.
func AddWorkspaceFiles(home string, rel ...string) error {
	var w workspaceFiles
	loadJSON(WorkspaceFilesPath(home), &w)
	have := map[string]bool{}
	for _, p := range w.Paths {
		have[p] = true
	}
	for _, p := range rel {
		p = filepath.ToSlash(filepath.Clean(p))
		if p == "." || strings.HasPrefix(p, "../") || filepath.IsAbs(p) || have[p] {
			continue
		}
		w.Paths, have[p] = append(w.Paths, p), true
	}
	sort.Strings(w.Paths)
	b, err := jsonIndent(w)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(LibraryDir(home), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(WorkspaceFilesPath(home), b, 0o644)
}

// workspaceAdapter syncs the configured paths of the current workspace,
// re-read on every scan.
func workspaceAdapter(home string) adapter.Adapter {
	return adapter.WorkspaceFiles(func() (string, []string) {
		ws := paths.Workspace()
		if ws == "" {
			return "", nil
		}
		return ws, WorkspaceFileList(home)
	})
}

// workspaceCandidates are personal files agents and tools keep in a
// workspace (relative path, description). Directories sync recursively.
var workspaceCandidates = []struct{ rel, what string }{
	{".claude/memory", "Claude Code memory notes"},
	{".comate/memory", "Comate memory notes"},
	{".codex/memories", "Codex memories"},
	{".memory", "memory MCP knowledge graph"},
	{".claude/settings.json", "Claude Code project settings"},
	{".claude/settings.local.json", "Claude Code local settings (permissions)"},
	{".claude/CLAUDE.md", "Claude Code instructions"},
	{"CLAUDE.md", "Claude Code instructions"},
	{"AGENTS.md", "Codex / agent instructions"},
	{".codex/config.toml", "Codex project config"},
	{".codex/AGENTS.md", "Codex instructions"},
	{".comate/mcp.json", "Comate MCP servers (tokens stay encrypted)"},
	{".mcp.json", "MCP servers of the workspace (tokens stay encrypted)"},
	{".ssh/known_hosts", "SSH known hosts"},
	{".ssh/ssh-proxy", "SSH proxy script"},
	{".popo.json", "popo deployment config"},
}

// discoverWorkspaceFiles returns workspace files worth keeping that are
// not in the list yet.
func discoverWorkspaceFiles(home, ws string) []Finding {
	if ws == "" {
		return nil
	}
	have := map[string]bool{}
	for _, p := range WorkspaceFileList(home) {
		have[p] = true
	}
	var out []Finding
	for _, c := range workspaceCandidates {
		if have[c.rel] {
			continue
		}
		p := filepath.Join(ws, filepath.FromSlash(c.rel))
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		detail := "${WORKSPACE}/" + c.rel
		if fi.IsDir() {
			n := 0
			filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					n++
				}
				return nil
			})
			if n == 0 {
				continue
			}
			detail += "/ (" + strconv.Itoa(n) + " files)"
		}
		out = append(out, Finding{ID: "wsfile:" + c.rel, Kind: "workspace", Title: c.what, Detail: detail, wsFile: c.rel})
	}
	return out
}
