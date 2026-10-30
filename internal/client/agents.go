package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/mcpcfg"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// SupportedAgents are the agents hearth can integrate with. Codex and
// Claude Code are enabled by default; others are opt-in.
var SupportedAgents = []string{"codex", "claude", "comate"}

// DefaultAgents are enabled when nothing is configured.
var DefaultAgents = []string{"codex", "claude"}

type agentsFile struct {
	Enabled []string          `json:"enabled,omitempty"`
	Homes   map[string]string `json:"homes,omitempty"` // agent -> config dir, e.g. ${WORKSPACE}/.codex
}

// agentHomeEnv is the variable each agent reads its config dir from.
var agentHomeEnv = map[string]string{"codex": "CODEX_HOME", "claude": "CLAUDE_CONFIG_DIR", "comate": "HEARTH_COMATE_HOME"}

// AgentHomes returns per-user agent config dirs from config.toml, falling
// back to the old library/agents.json.
func AgentHomes(home string) map[string]string {
	out := map[string]string{}
	if c, err := UserConfig(home); err == nil && c.Agents != nil && len(c.Agents.Homes) > 0 {
		for k, v := range c.Agents.Homes {
			out[k] = v
		}
	} else {
		var f agentsFile
		loadJSON(agentsPath(home), &f)
		for k, v := range f.Homes {
			out[k] = v
		}
	}
	// Chosen on this machine (hearth agents isolate): wins over the synced one.
	for k, v := range LocalAgentHomes(home) {
		out[k] = v
	}
	return out
}

// SetAgentHome records where an agent keeps its config for this user and
// writes the recipe that exports it inside the workspace.
func SetAgentHome(home, agent, dir string) error {
	if _, ok := agentHomeEnv[agent]; !ok {
		return fmt.Errorf("unknown agent %q", agent)
	}
	if !paths.Portable(dir) {
		return fmt.Errorf("%q must be absolute or start with ~/, ${HOME}/ or ${WORKSPACE}/", dir)
	}
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	if c.Agents == nil {
		c.Agents = &library.AgentsConfig{}
	}
	if c.Agents.Homes == nil {
		c.Agents.Homes = map[string]string{}
	}
	c.Agents.Homes[agent] = dir
	if err := SaveUserConfig(home, c); err != nil {
		return err
	}
	os.Remove(agentsPath(home))
	return writeAgentHomesRecipe(home, c.Agents.Homes)
}

// writeAgentHomesRecipe keeps recipes/agent-homes.toml in step with the
// configured homes: one env step, scoped to the workspace when there is one.
func writeAgentHomesRecipe(home string, homes map[string]string) error {
	var b strings.Builder
	b.WriteString("# Managed by `hearth agents home`; edits are overwritten.\nname = \"agent-homes\"\ndescription = \"Per-user agent config directories\"\n\n[[step]]\ntype = \"env\"\n")
	if WorkspaceOf(home) != "" {
		b.WriteString("scope = [\"${WORKSPACE}\"]\n")
	}
	b.WriteString("set = { ")
	var parts []string
	for _, agent := range SupportedAgents {
		if p, ok := homes[agent]; ok {
			parts = append(parts, fmt.Sprintf("%s = %q", agentHomeEnv[agent], p))
		}
	}
	b.WriteString(strings.Join(parts, ", ") + " }\n")
	dir := RecipesDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "agent-homes.toml"), []byte(b.String()), 0o644)
}

func agentsPath(home string) string { return filepath.Join(LibraryDir(home), "agents.json") }

// EnabledAgents returns $HEARTH_AGENTS (comma separated), else the
// synced library/agents.json, else DefaultAgents.
func EnabledAgents(home string) []string {
	if v := os.Getenv("HEARTH_AGENTS"); v != "" {
		return strings.Split(v, ",")
	}
	if c, err := UserConfig(home); err == nil && c.Agents != nil && len(c.Agents.Enabled) > 0 {
		return c.Agents.Enabled
	}
	var f agentsFile
	if err := loadJSON(agentsPath(home), &f); err == nil && len(f.Enabled) > 0 {
		return f.Enabled
	}
	return DefaultAgents
}

// SetAgentEnabled enables or disables an agent (synced setting).
func SetAgentEnabled(home, name string, on bool) error {
	ok := false
	for _, a := range SupportedAgents {
		if a == name {
			ok = true
		}
	}
	if !ok {
		return fmt.Errorf("unknown agent %q (supported: %s)", name, strings.Join(SupportedAgents, ", "))
	}
	var out []string
	for _, a := range EnabledAgents(home) {
		if a != name {
			out = append(out, a)
		}
	}
	if on {
		out = append(out, name)
	}
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	if c.Agents == nil {
		c.Agents = &library.AgentsConfig{}
	}
	c.Agents.Enabled = out
	if err := SaveUserConfig(home, c); err != nil {
		return err
	}
	os.Remove(agentsPath(home))
	return nil
}

// localAgentsFile lists the agents restored on this machine. It is not
// synced: a new machine restores only the agents the user worked with
// recently (setup writes it) and the rest on request (hearth agents pull).
type localAgentsFile struct {
	Restore []string `json:"restore"`
}

func localAgentsPath(home string) string { return filepath.Join(home, "agents.local.json") }

// MachineAgents returns the enabled agents restored on this machine: the
// local selection if setup made one, else every enabled agent.
func MachineAgents(home string) []string {
	var f localAgentsFile
	if err := loadJSON(localAgentsPath(home), &f); err != nil || f.Restore == nil {
		return EnabledAgents(home)
	}
	on := enabledSet(home)
	var out []string
	for _, a := range f.Restore {
		if on[a] {
			out = append(out, a)
		}
	}
	return out
}

// HasMachineAgents reports whether this machine has its own selection.
func HasMachineAgents(home string) bool {
	_, err := os.Stat(localAgentsPath(home))
	return err == nil
}

// SetMachineAgents records which agents this machine restores.
func SetMachineAgents(home string, agents []string) error {
	seen := map[string]bool{}
	f := localAgentsFile{Restore: []string{}}
	for _, a := range agents {
		if a = strings.TrimSpace(a); a != "" && !seen[a] {
			seen[a] = true
			f.Restore = append(f.Restore, a)
		}
	}
	b, err := jsonIndent(f)
	if err != nil {
		return err
	}
	return writeFileAtomic(localAgentsPath(home), b, 0o644)
}

// AddMachineAgent adds one agent to this machine's selection.
func AddMachineAgent(home, agent string) error {
	return SetMachineAgents(home, append(MachineAgents(home), agent))
}

func machineSet(home string) map[string]bool {
	m := map[string]bool{}
	for _, a := range MachineAgents(home) {
		m[a] = true
	}
	return m
}

func enabledSet(home string) map[string]bool {
	m := map[string]bool{}
	for _, a := range EnabledAgents(home) {
		m[strings.TrimSpace(a)] = true
	}
	return m
}

// EnabledAdapters filters session adapters by the enabled agents.
func EnabledAdapters(home string) []adapter.Adapter {
	on := enabledSet(home)
	var out []adapter.Adapter
	for _, a := range adapter.All() {
		if on[a.Name()] {
			out = append(out, a)
		}
	}
	return append(out, workspaceAdapter(home), selectedAdapter(home))
}

// EnabledLibTargets filters library render targets by the enabled agents.
// Only this machine's agents (MachineAgents) are rendered into.
func EnabledLibTargets(home string) []library.Target {
	on := machineSet(home)
	var out []library.Target
	for _, t := range library.DefaultTargets() {
		if on[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// EnabledMCPTargets filters MCP render targets by the enabled agents.
func EnabledMCPTargets(home string) []mcpcfg.Target {
	on := machineSet(home)
	var out []mcpcfg.Target
	for _, t := range mcpcfg.DefaultTargets() {
		if on[t.Agent] {
			out = append(out, t)
		}
	}
	return out
}
