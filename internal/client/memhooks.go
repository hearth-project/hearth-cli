package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Memory hooks: Codex (hooks.json in its home) and Claude Code
// (settings.json) run a command before each prompt and at session start
// and add what it prints to the model's context. Hearth registers
// `hearth recall --hook` there, so relevant memories reach the agent
// without the model having to decide to look. Other hooks in those files
// are left alone; Hearth's own entry is recognised by its command.

const hookMarker = "recall --hook"

// MemoryHooksOn reports [memory] hooks (default true, needs memory on).
func MemoryHooksOn(home string) bool {
	if !MemoryOn(home) {
		return false
	}
	c, err := UserConfig(home)
	if err != nil || c.Memory == nil || c.Memory.Hooks == nil {
		return true
	}
	return *c.Memory.Hooks
}

// HookResult is one agent's hook file.
type HookResult struct {
	Agent, Path, Op string // installed | unchanged | removed | skipped
	Detail          string
}

// hookCommand names no HEARTH_HOME for ~/.hearth and workspaces: the hook
// answers from the hearth home of the session's working directory
// (HomeFor), so one hooks file shared by several people on a Unix account
// gives each their own memories. A home elsewhere is named.
func hookCommand(home, exe string) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	if home != "" && !FoundHome(home) {
		return "HEARTH_HOME=" + q(home) + " " + q(exe) + " " + hookMarker
	}
	return q(exe) + " " + hookMarker
}

// FoundHome reports whether home is one HomeFor finds by itself: ~/.hearth
// or a workspace's.
func FoundHome(home string) bool {
	return filepath.Clean(home) == filepath.Clean(DefaultHome()) || WorkspaceOf(home) != ""
}

// HomeFor is the hearth home that answers for an agent session run in
// cwd: the workspace containing cwd, else ~/.hearth (the home directory is
// the user's who logged in without a workspace). Agent config dirs are often
// shared by several people (one Unix account), so a hook or MCP server
// registered there must not answer with the memories of whoever installed it.
func HomeFor(cwd string) string {
	if cwd != "" {
		if ws := FindWorkspace(cwd); ws != "" {
			return WorkspaceHome(ws)
		}
	}
	return DefaultHome()
}

// hookFiles are the hook configuration files of the agents enabled here.
func hookFiles(home string) map[string]string {
	out := map[string]string{}
	for _, t := range EnabledLibTargets(home) {
		switch t.Name {
		case "codex":
			out["codex"] = filepath.Join(t.Root, "hooks.json")
		case "claude":
			out["claude"] = filepath.Join(t.Root, "settings.json")
		}
	}
	return out
}

// SyncMemoryHooks installs (on) or removes (off) Hearth's hooks in every
// enabled agent that supports them.
func SyncMemoryHooks(home string, on bool) ([]HookResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	cmd := hookCommand(home, exe)
	var out []HookResult
	files := hookFiles(home)
	for _, agent := range []string{"codex", "claude"} {
		p, ok := files[agent]
		if !ok {
			continue
		}
		if _, err := os.Stat(filepath.Dir(p)); err != nil {
			out = append(out, HookResult{Agent: agent, Path: p, Op: "skipped", Detail: "agent not set up here"})
			continue
		}
		op, err := editHooks(p, cmd, on)
		r := HookResult{Agent: agent, Path: p, Op: op}
		if err != nil {
			r.Op, r.Detail = "skipped", err.Error()
		}
		out = append(out, r)
	}
	return out, nil
}

// editHooks rewrites the "hooks" object of a JSON settings file: Hearth's
// SessionStart and UserPromptSubmit entries are replaced (on) or dropped.
func editHooks(path, command string, on bool) (string, error) {
	doc := map[string]any{}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if !on {
			return "unchanged", nil
		}
	case err != nil:
		return "", err
	default:
		if len(strings.TrimSpace(string(b))) > 0 {
			if err := json.Unmarshal(b, &doc); err != nil {
				return "", errors.New("not valid JSON; left alone")
			}
		}
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		groups, _ := hooks[event].([]any)
		var keep []any
		for _, g := range groups {
			if !hearthGroup(g) {
				keep = append(keep, g)
			}
		}
		if on {
			keep = append(keep, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 5}}})
		}
		if len(keep) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keep
		}
	}
	if len(hooks) == 0 {
		delete(doc, "hooks")
	} else {
		doc["hooks"] = hooks
	}
	nb, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	nb = append(nb, '\n')
	if string(nb) == string(b) || (!on && len(b) == 0) {
		return "unchanged", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := writeFileAtomic(path, nb, mode); err != nil {
		return "", err
	}
	if on {
		return "installed", nil
	}
	return "removed", nil
}

func hearthGroup(g any) bool {
	m, _ := g.(map[string]any)
	hs, _ := m["hooks"].([]any)
	for _, h := range hs {
		hm, _ := h.(map[string]any)
		if c, _ := hm["command"].(string); strings.Contains(c, hookMarker) && strings.Contains(c, "hearth") {
			return true
		}
	}
	return false
}

// SetMemoryConfig changes [memory] enabled / hooks (nil = unchanged).
func SetMemoryConfig(home string, enabled, hooks *bool) error {
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	if c.Memory == nil {
		c.Memory = &library.MemoryConfig{}
	}
	if enabled != nil {
		c.Memory.Enabled = enabled
	}
	if hooks != nil {
		c.Memory.Hooks = hooks
	}
	return SaveUserConfig(home, c)
}

// MemoryHookStatus reports, per enabled agent, whether Hearth's prompt
// hook is in its hook file (without changing anything).
func MemoryHookStatus(home string) []HookResult {
	var out []HookResult
	files := hookFiles(home)
	for _, agent := range []string{"codex", "claude"} {
		p, ok := files[agent]
		if !ok {
			continue
		}
		r := HookResult{Agent: agent, Path: p, Op: "missing"}
		if b, err := os.ReadFile(p); err == nil {
			var doc map[string]any
			if json.Unmarshal(b, &doc) == nil {
				hooks, _ := doc["hooks"].(map[string]any)
				groups, _ := hooks["UserPromptSubmit"].([]any)
				for _, g := range groups {
					if hearthGroup(g) {
						r.Op = "installed"
					}
				}
			}
		}
		out = append(out, r)
	}
	return out
}
