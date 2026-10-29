package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/hearth-project/hearth-cli/internal/paths"
)

// Config is <library>/config.toml, the one file a user edits. Sections
// that are absent fall back to the legacy per-feature file (if any) so an
// existing library keeps working; MigrateLegacy folds those into here.
type Config struct {
	Skills     *SkillsConfig     `toml:"skills,omitempty"`
	Agents     *AgentsConfig     `toml:"agents,omitempty"`
	Backup     *BackupConfig     `toml:"backup,omitempty"`
	Learn      *LearnConfig      `toml:"learn,omitempty"`
	Tools      *ToolsConfig      `toml:"tools,omitempty"`
	Workspaces *WorkspacesConfig `toml:"workspaces,omitempty"`
	MCP        *MCPConfig        `toml:"mcp,omitempty"`
	Usage      *UsageConfig      `toml:"usage,omitempty"`
	Recipes    *RecipesConfig    `toml:"recipes,omitempty"`
	Teams      *TeamsConfig      `toml:"teams,omitempty"`
	Shell      *ShellConfig      `toml:"shell,omitempty"`
	Memory     *MemoryConfig     `toml:"memory,omitempty"`
	SSH        *SSHConfig        `toml:"ssh,omitempty"`
}

// SSHConfig: machines accept SSH from the account's own devices, with
// Hearth managing keys and host keys.
type SSHConfig struct {
	// Enabled for every machine of the account (default: on for Linux);
	// `hearth ssh disable` turns one machine off.
	Enabled *bool  `toml:"enabled,omitempty"`
	Port    int    `toml:"port,omitempty"`    // first port to try (default 8022)
	Address string `toml:"address,omitempty"` // listen address (default: bond0, else the main interface)
	// ShareKeys: also accept each device's usual keys (~/.ssh/id_*.pub),
	// so plain ssh and IDEs work (default true).
	ShareKeys *bool `toml:"share_keys,omitempty"`
}

// MemoryConfig: long-term memory (conversation cards, remembered facts,
// agents' notes) searched by every agent.
type MemoryConfig struct {
	// Enabled turns the index, recall and the agent hooks on (default true).
	Enabled *bool `toml:"enabled,omitempty"`
	// Hooks installs the prompt hooks that inject relevant memories into
	// Codex and Claude Code (default true).
	Hooks *bool `toml:"hooks,omitempty"`
}

type TeamsConfig struct {
	// ShareTop shares the user's most used skills and agents (this many of
	// each, from the account-wide ranking) with every team where the user
	// may publish; nil = 5, 0 = off.
	ShareTop *int `toml:"share_top,omitempty"`
	// ShareExclude names skills/agents never shared automatically.
	ShareExclude []string `toml:"share_exclude,omitempty"`
	// UseTop keeps each team's most used skills and agents (this many of
	// each, ranked by the members' use) in every agent on every machine,
	// like the user's own; nil = 20, 0 = off. The rest of a team's items
	// stay reachable through skill search.
	UseTop *int `toml:"use_top,omitempty"`
	// UseExclude names team skills/agents never kept automatically
	// (unlinking an automatic one adds it here).
	UseExclude []string `toml:"use_exclude,omitempty"`
}

type RecipesConfig struct {
	// ShareApprovals: a recipe command approved on one machine runs on the
	// others without asking again (default true). The approvals are kept
	// end-to-end encrypted, so the server cannot approve anything itself.
	ShareApprovals *bool `toml:"share_approvals,omitempty"`
}

type SkillsConfig struct {
	// Dir is where the single copy of every skill lives. Portable:
	// absolute, ~/..., ${HOME}/... or ${WORKSPACE}/...
	Dir string `toml:"dir,omitempty"`
	// Resident lists the skills rendered into every agent. Empty = all;
	// the rest stay available on demand through `hearth mcp`.
	Resident []string `toml:"resident,omitempty"`
}

type AgentsConfig struct {
	Enabled []string          `toml:"enabled,omitempty"`
	Homes   map[string]string `toml:"homes,omitempty"`
}

type BackupConfig struct {
	// Categories lists every kind of data Hearth can upload and whether it
	// is on. true = upload, false = never; a category missing here keeps
	// the default (on).
	Categories map[string]bool `toml:"categories,omitempty"`
	// Include adds files/paths, Exclude turns paths off.
	Include []string `toml:"include,omitempty"`
	Exclude []string `toml:"exclude,omitempty"`
}

type LearnConfig struct {
	// Ignore lists findings the user never wants to be asked about again.
	Ignore []string `toml:"ignore,omitempty"`
	// Auto: the background sync records new findings by itself, with the
	// proposal `hearth learn` would make (default true). Credentials follow
	// the backup selection (@credentials).
	Auto *bool `toml:"auto,omitempty"`
	// AutoCredentials: automatic learning also uploads credentials (SSH
	// keys, agent and git logins, token files; end-to-end encrypted). nil =
	// not decided yet (setup asks once); only with @credentials on.
	AutoCredentials *bool `toml:"auto_credentials,omitempty"`
}

// ShellConfig: the user's own lines of ~/.bashrc / ~/.zshrc (and the whole
// ~/.bash_aliases) are kept encrypted and sourced on new machines.
type ShellConfig struct {
	// Sync turns it on or off (default true).
	Sync *bool `toml:"sync,omitempty"`
	// Files overrides the rc files read (default ~/.bashrc, ~/.zshrc,
	// ~/.bash_aliases).
	Files []string `toml:"files,omitempty"`
}

type ToolsConfig struct {
	Pins   []ToolPin    `toml:"pin,omitempty"`
	Custom []ToolCustom `toml:"custom,omitempty"`
}

type ToolPin struct {
	Name    string `toml:"name"`
	Version string `toml:"version,omitempty"`
}

type ToolCustom struct {
	Name    string `toml:"name"`
	Version string `toml:"version,omitempty"`
	Check   string `toml:"check,omitempty"`
	Install string `toml:"install,omitempty"`
}

// WorkspacesConfig is the setting of the removed uncommitted-work
// snapshots; kept so older config files still parse (migration drops it).
type WorkspacesConfig struct {
	Roots        []string `toml:"roots,omitempty"`
	MaxDepth     int      `toml:"max_depth,omitempty"`
	MaxFileBytes int64    `toml:"max_file_bytes,omitempty"`
	MaxTotal     int64    `toml:"max_total_bytes,omitempty"`
}

type MCPConfig struct {
	Servers map[string]MCPServer `toml:"servers,omitempty"`
}

type UsageConfig struct {
	// ShareWith lists teams whose published skills get this machine's
	// usage counts (opt-in, as before). The user's own ranking is uploaded
	// separately so `resident` can follow actual use.
	ShareWith []string `toml:"share_with,omitempty"`
}

type MCPServer struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args,omitempty"`
	Env     map[string]string `toml:"env,omitempty"`
	Agents  []string          `toml:"agents,omitempty"`
}

// ConfigPath is the library's own configuration file (synced with it).
func (l Library) ConfigPath() string { return filepath.Join(l.Root, "config.toml") }

// LoadConfig reads config.toml; a missing file is the default layout.
func (l Library) LoadConfig() (Config, error) {
	var c Config
	b, err := os.ReadFile(l.ConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := toml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", l.ConfigPath(), err)
	}
	if c.Skills != nil {
		if d := strings.TrimSpace(c.Skills.Dir); d != "" && !paths.Portable(d) {
			return c, fmt.Errorf("%s: skills.dir %q must be absolute or start with ~/, ${HOME}/ or ${WORKSPACE}/", l.ConfigPath(), d)
		}
	}
	if c.Agents != nil {
		for a, p := range c.Agents.Homes {
			if !paths.Portable(p) {
				return c, fmt.Errorf("%s: agents.homes.%s %q must be absolute or start with ~/, ${HOME}/ or ${WORKSPACE}/", l.ConfigPath(), a, p)
			}
		}
	}
	if c.MCP != nil {
		for n, s := range c.MCP.Servers {
			if n == "" || s.Command == "" {
				return c, fmt.Errorf("%s: mcp.servers.%s: name and command are required", l.ConfigPath(), n)
			}
		}
	}
	return c, nil
}

// SaveConfig writes config.toml atomically.
func (l Library) SaveConfig(c Config) error {
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	b = append([]byte("# Hearth settings — edit with `hearth config --edit`. Every section is optional.\n"+
		"# Docs: https://github.com/hearth-project/hearth-cli/blob/main/docs/en/usage.md\n\n"), b...)
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		return err
	}
	tmp := l.ConfigPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.ConfigPath())
}

// Template is the commented starter file a new library gets. Kept short:
// every section is optional and the defaults are listed in comments.
const Template = `# Hearth settings — the only file you need to edit. It syncs with your
# library, so every machine gets the same choices.
#
# Machine-local things are not here: the login (config.json in the Hearth
# home), the daemon's auto-update switch and which agents this machine
# restored are per machine.

[skills]
# Where the single copy of the skills lives (default: <library>/skills).
# Portable: absolute, ~/..., ${HOME}/..., ${WORKSPACE}/...
# dir = "${WORKSPACE}/.agents/skills"
# Render only these into every agent; the rest are found on demand by the
# skill-search MCP. Empty/absent = all resident.
# resident = ["benchmark", "lint"]

[agents]
# Agents Hearth integrates with on all your machines. Default: codex, claude.
# enabled = ["codex", "claude"]
# Per-user agent config dirs (same as "hearth agents home").
# [agents.homes]
# codex = "${WORKSPACE}/.codex"
# claude = "${WORKSPACE}/.claude"

[backup]
# Everything Hearth can upload is listed here; set false to stop uploading
# that kind. Remove a line to use the default (on).
[backup.categories]
"codex" = true
"codex.sessions" = true
"codex.memory" = true
"claude" = true
"claude.sessions" = true
"claude.memory" = true
"comate" = true
"comate.sessions" = true
"comate.memory" = true
"workspace" = true
"credentials" = true
# Extra files/directories (~/, ${WORKSPACE}/, * ** ?; trailing / = dir).
# include = ["${WORKSPACE}/notes/", "~/.gitconfig"]
# Exclusions win over categories and includes.
# exclude = ["**/*.tmp", "~/.ssh/id_rsa"]

[learn]
# Findings you never want to be asked about again.
# ignore = ["tool:dumate"]
# The background sync records new findings by itself (credentials only
# while @credentials is on in [backup]); false = only with "hearth learn".
# auto = true
# Also SSH keys, agent / git logins and token files (encrypted); setup asks.
# auto_credentials = true

[memory]
# Long-term memory: cards of your conversations on every machine, facts you
# asked agents to remember and the agents' own notes, searched locally in
# milliseconds. Hooks add the most relevant ones to each prompt of Codex and
# Claude Code (Codex asks once to trust the hook).
# enabled = true
# hooks = true

[ssh]
# Your Linux machines accept SSH from your own devices: the keys of your
# devices go into ~/.ssh/authorized_keys (a marked block; your own lines
# stay) and sshd runs with public keys only on the main interface, port
# 8022 (an sshd already there is used as it is). Connect from any device:
# hearth ssh <machine>, or plain ssh -p 8022 user@address with your usual
# key. One machine off: hearth ssh disable.
# enabled = true
# port = 8022
# share_keys = true   # also accept each device's ~/.ssh/id_*.pub

[shell]
# Your own lines of ~/.bashrc / ~/.zshrc (and ~/.bash_aliases) are kept
# encrypted and sourced on new machines (lines of the distribution's default
# rc and Hearth's own blocks are left out). false = do not keep them.
# sync = true
# files = ["~/.bashrc", "~/.zshrc", "~/.bash_aliases"]

[tools]
# Pin agent CLIs (installed only when missing; "hearth tools install" upgrades).
# [[tools.pin]]
# name = "codex"
# version = "0.163.0"
# Custom tools run install/check commands only after approval on each machine.
# [[tools.custom]]
# name = "dumate"
# check = "dumate --version"
# install = "pip install dumate==${VERSION}"

[recipes]
# A command of a restore recipe approved on one machine runs on your other
# machines without asking again (approvals are end-to-end encrypted; a
# changed command is asked about again). false = approve on every machine.
# share_approvals = true

[teams]
# Your 5 most used skills and 5 most used agents (all your machines, last 30
# days) are shared with every team where you can publish, and kept up to date.
# share_top = 5            # 0 = do not share anything automatically
# share_exclude = ["private-notes"]
# Each team's 20 most used skills and 20 most used agents (by its members) are
# kept in all your agents like your own; the rest are found by skill search.
# use_top = 20             # 0 = only through skill search
# use_exclude = ["some-team-skill"]

[usage]
# Teams whose published skills get this machine's usage counts (opt-in).
# share_with = ["my-team"]

# MCP servers rendered into the agents; ${secret:NAME} resolves a vault file.
# [mcp.servers.example]
# command = "npx"
# args = ["-y", "@modelcontextprotocol/server-filesystem", "${WORKSPACE}"]
# agents = ["codex", "claude"]
`
