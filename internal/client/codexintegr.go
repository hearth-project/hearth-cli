package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/adapter"
)

// codexIntegration implements AgentIntegration for Codex. All Codex-only
// behaviour lives here (or in codexstate/codexdesktop/codeximport); the
// sync and restore core is agent-neutral.
type codexIntegration struct{}

func (codexIntegration) Name() string { return "codex" }

func (codexIntegration) Capabilities() AgentCapabilities {
	return AgentCapabilities{
		Sessions: true, Memory: true, Threads: true, LiveUI: true, DeleteSync: true, PathMap: true,
		ImportFrom:  []string{"claude"},
		IndexFields: []string{"title", "turns", "last_active", "writer", "thread_name", "section"},
	}
}

func (codexIntegration) ConversationKey(relPath string) string {
	if id := adapter.CodexThreadID(relPath); id != "" {
		return id
	}
	return ""
}

func (codexIntegration) Prepare(e *Engine) any {
	known, _ := adapter.CodexKnownThreads(e.CodexHome())
	if known == nil {
		known = map[string]bool{}
	}
	return known
}

func (codexIntegration) IsPart(relPath string) bool {
	id := adapter.CodexThreadID(relPath)
	return id != "" && strings.Contains(strings.TrimSuffix(filepath.Base(relPath), ".jsonl"), id+"_")
}

// AfterRestore restores fork parents, registers restored threads in Codex's
// index (names/sections) and, when Desktop is running, shows them in the
// open window. Fork parents the user did not ask for are archived: Codex
// still reads their history for the fork, but they do not crowd the sidebar.
func (in codexIntegration) AfterRestore(e *Engine, all []RemoteSession, restored []RestoredSession, opts RestoreOptions) error {
	var changed, parents []string
	requested := map[string]bool{}
	var unarchive []string
	for _, r := range restored {
		id := adapter.CodexThreadID(r.Result.Path)
		if id != "" && !requested[id] {
			requested[id] = true
			// Asking for an archived thread by id (e.g. a fork parent an
			// earlier restore archived) lists it again, unless a Desktop copy
			// of it is what the sidebar shows.
			if prev := e.DesktopForkOf(id); opts.Explicit && codexArchivedPath(r.Result.Path) &&
				(prev == "" || !CodexRolloutExists(e.CodexHome(), prev)) {
				unarchive = append(unarchive, id)
			}
		}
	}
	if n, err := SetCodexThreadsArchived(e.CodexHome(), unarchive, false); err != nil {
		opts.printf("codex threads: %v\n", err)
	} else if n > 0 {
		opts.printf("codex threads: unarchived %d\n", n)
	}
	for _, r := range restored {
		if r.Result.Action == "up-to-date" || r.Result.Action == "skipped" || r.Result.Path == "" {
			continue
		}
		parents = append(parents, in.restoreParents(e, r.Result.Path, opts)...)
		changed = append(changed, r.Result.Path)
	}
	if len(changed) == 0 {
		return nil
	}
	st, err := e.ApplyAgentState(all, append(changed, parents...)...)
	if st.Indexed+st.Named+st.Sectioned+st.Missing > 0 {
		opts.printf("codex threads: indexed=%d renamed=%d moved-to-section=%d unchanged=%d not-found=%d\n", st.Indexed, st.Named, st.Sectioned, st.Unchanged, st.Missing)
	}
	if err != nil {
		opts.printf("codex threads: %v (re-run later: hearth codex-threads)\n", err)
	}
	seen, shown := map[string]bool{}, 0
	for _, r := range restored {
		if r.Result.Action == "up-to-date" || r.Result.Action == "skipped" || r.Result.Path == "" {
			continue
		}
		id := adapter.CodexThreadID(r.Result.Path)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if in.showInDesktop(e, r.Remote, r.Result, opts) {
			shown++
		}
	}
	if shown > 0 && !opts.Explicit {
		opts.printf("codex desktop: %d restored conversations shown in the open window (the restored originals are archived)\n", shown)
	}
	in.archiveParents(e, parents, requested, opts)
	return err
}

// archiveParents archives the fork parents restored only for their history.
func (codexIntegration) archiveParents(e *Engine, parents []string, requested map[string]bool, opts RestoreOptions) {
	seen := map[string]bool{}
	var ids []string
	for _, p := range parents {
		if id := adapter.CodexThreadID(p); id != "" && !requested[id] && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	n, err := SetCodexThreadsArchived(e.CodexHome(), ids, true)
	if n > 0 {
		opts.printf("codex threads: archived %d fork parent(s) restored for their history; restore one by id to list it again\n", n)
	}
	if err != nil {
		opts.printf("codex threads: archiving fork parents: %v\n", err)
	}
}

func codexArchivedPath(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/archived_sessions/")
}

// restoreParents restores the threads a Codex fork builds on (a fork
// rollout references its parent's history instead of copying it) and
// returns the paths it wrote.
func (codexIntegration) restoreParents(e *Engine, path string, opts RestoreOptions) []string {
	home := e.CodexHome()
	var all []RemoteSession
	var restored []string
	for depth := 0; depth < 32; depth++ {
		parent := CodexForkParent(path)
		if parent == "" || CodexRolloutExists(home, parent) {
			return restored
		}
		if all == nil {
			var err error
			if all, err = e.List(); err != nil {
				opts.printf("fork parent: %v\n", err)
				return restored
			}
		}
		found := false
		for _, p := range all {
			if p.Meta.Agent == "codex" && adapter.CodexThreadID(p.Meta.RelPath) == parent {
				r, err := e.Restore(p, false)
				if err != nil {
					opts.printf("fork parent %s: %v\n", parent, err)
					return restored
				}
				opts.printf("%s  %s %s (fork parent)\n", shortID(p.ID), r.Action, r.Path)
				path, found = r.Path, true
				restored = append(restored, r.Path)
				break
			}
		}
		if !found {
			opts.printf("fork parent %s is not on the server; Codex shows this thread without the forked history\n", parent)
			return restored
		}
	}
	return restored
}

// showInDesktop reports whether it added the conversation to the open
// window; a bulk restore prints one summary line instead of one per thread.
func (codexIntegration) showInDesktop(e *Engine, rs RemoteSession, r RestoreResult, opts RestoreOptions) bool {
	id := adapter.CodexThreadID(r.Path)
	home := e.CodexHome()
	if id == "" || home == "" || (codexIntegration{}).IsPart(r.Path) {
		return false
	}
	if prev := e.DesktopForkOf(id); prev != "" && CodexRolloutExists(home, prev) {
		if opts.Explicit {
			opts.printf("already in the Codex Desktop sidebar as thread %s\n", prev)
		}
		return false
	}
	known, _ := opts.Prepared["codex"].(map[string]bool)
	if !opts.Explicit && known[id] {
		return false
	}
	f, err := ShowInDesktop(home, id, "", rs.Meta.Thread)
	if errors.Is(err, ErrNoDesktop) {
		return false // Desktop lists it when it starts
	}
	if f.Fork != "" {
		if err := e.RecordDesktopFork(f); err != nil {
			opts.printf("codex desktop: %v\n", err)
		}
		if opts.Explicit {
			opts.printf("shown in the open Codex Desktop window as thread %s (the restored original is archived)\n", f.Fork)
		}
	}
	if err != nil {
		opts.printf("codex desktop: %v\n", err)
	}
	return f.Fork != ""
}

func (codexIntegration) SessionCwd(absPath string) string {
	return firstJSONField(absPath, func(m map[string]json.RawMessage) string {
		var p struct {
			Cwd string `json:"cwd"`
		}
		if raw, ok := m["payload"]; ok && json.Unmarshal(raw, &p) == nil {
			return p.Cwd
		}
		return ""
	})
}

func (codexIntegration) RestorePath(root, rel, want string) (string, bool) {
	return codexMovedPath(root, rel, want)
}

func (codexIntegration) MovedSession(e *Engine, logical string) string {
	if strings.HasPrefix(logical, "codex/") {
		return e.st.codexMoved(logical)
	}
	return ""
}

func (codexIntegration) ExpireDeleted(e *Engine, present map[string]bool) (int, error) {
	return e.expireDeleted(present)
}

func (codexIntegration) Import(e *Engine, sourceAgent string, paths []string) ([]ImportResult, bool, error) {
	if sourceAgent != "claude" {
		return nil, false, fmt.Errorf("codex can import claude sessions, not %s", sourceAgent)
	}
	home := e.CodexHome()
	if home == "" {
		return nil, false, errors.New("codex is not enabled for this workspace")
	}
	res, live, err := ImportClaudeIntoCodex(home, paths, nil)
	out := make([]ImportResult, 0, len(res))
	for _, r := range res {
		out = append(out, ImportResult{Source: r.Source, Thread: r.Thread, Title: r.Title, Error: r.Error})
	}
	return out, live, err
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
