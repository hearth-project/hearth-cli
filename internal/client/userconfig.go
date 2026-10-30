package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/mcpcfg"
	"github.com/hearth-project/hearth-cli/internal/tools"
)

// UserConfig is the one editable file, <library>/config.toml. Machine-local
// state (login, auto-update, which agents this machine restored) stays in
// the Hearth home and is deliberately not here.
func UserConfig(home string) (library.Config, error) {
	return library.Library{Root: LibraryDir(home)}.LoadConfig()
}

func SaveUserConfig(home string, c library.Config) error {
	return library.Library{Root: LibraryDir(home)}.SaveConfig(c)
}

// EnsureUserConfig creates config.toml (and folds in the per-feature files
// of older versions) when it is missing. Returns true when created.
func EnsureUserConfig(home string) (bool, error) {
	lib := library.Library{Root: LibraryDir(home)}
	if _, err := os.Stat(lib.ConfigPath()); err == nil {
		return false, MigrateUserConfig(home)
	}
	if err := os.MkdirAll(lib.Root, 0o755); err != nil {
		return false, err
	}
	// Fold the old files into an empty config first: the starter template
	// already lists every backup category, which would otherwise hide the
	// user's old exclusions.
	c := library.Config{}
	changed, err := migrateLegacy(home, &c)
	if err != nil {
		return false, err
	}
	if !changed {
		return true, os.WriteFile(lib.ConfigPath(), []byte(library.Template), 0o644)
	}
	if err := lib.SaveConfig(c); err != nil {
		return false, err
	}
	return true, nil
}

// Section-present helpers: the starter template has empty [agents] etc.
// headings, so a non-nil pointer alone does not mean the user set it.
func agentsSet(c *library.Config) bool {
	return c.Agents != nil && (len(c.Agents.Enabled) > 0 || len(c.Agents.Homes) > 0)
}
func skillsSet(c *library.Config) bool {
	return c.Skills != nil && (strings.TrimSpace(c.Skills.Dir) != "" || len(c.Skills.Resident) > 0)
}
func backupSet(c *library.Config) bool {
	return c.Backup != nil && (len(c.Backup.Categories) > 0 || len(c.Backup.Include) > 0 || len(c.Backup.Exclude) > 0)
}

// defaultCategories is the full catalog of uploadable kinds on this machine.
func defaultCategories() map[string]bool {
	m := map[string]bool{}
	for _, c := range BackupCategories() {
		m[c] = true
	}
	return m
}
func learnSet(c *library.Config) bool {
	return c.Learn != nil && len(c.Learn.Ignore) > 0
}
func toolsSet(c *library.Config) bool {
	return c.Tools != nil && (len(c.Tools.Pins) > 0 || len(c.Tools.Custom) > 0)
}
func mcpSet(c *library.Config) bool {
	return c.MCP != nil && len(c.MCP.Servers) > 0
}
func usageSet(c *library.Config) bool {
	return c.Usage != nil && len(c.Usage.ShareWith) > 0
}

// MigrateUserConfig folds the settings files older versions used
// (agents.json, backup.conf, tools.json, ...) into config.toml once and
// removes them, so there is a single file to edit. Missing sections keep
// their legacy file untouched (an old client may still write it).
func MigrateUserConfig(home string) error {
	lib := library.Library{Root: LibraryDir(home)}
	c, err := lib.LoadConfig()
	if err != nil {
		return err
	}
	changed, err := migrateLegacy(home, &c)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return lib.SaveConfig(c)
}

func migrateLegacy(home string, c *library.Config) (bool, error) {
	changed := false
	libDir := LibraryDir(home)
	del := func(name string) {
		if err := os.Remove(filepath.Join(libDir, name)); err == nil {
			changed = true
		}
	}

	if !agentsSet(c) {
		var f agentsFile
		if loadJSON(filepath.Join(libDir, "agents.json"), &f) == nil && (len(f.Enabled) > 0 || len(f.Homes) > 0) {
			c.Agents = &library.AgentsConfig{Enabled: f.Enabled, Homes: f.Homes}
			changed = true
		}
		del("agents.json")
	}

	if !skillsSet(c) {
		if b, err := os.ReadFile(filepath.Join(libDir, "resident.txt")); err == nil {
			var names []string
			for _, l := range strings.Split(string(b), "\n") {
				if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
					names = append(names, l)
				}
			}
			if len(names) > 0 {
				if c.Skills == nil {
					c.Skills = &library.SkillsConfig{}
				}
				c.Skills.Resident = names
				changed = true
			}
			del("resident.txt")
		}
	}

	if !backupSet(c) {
		if b, err := os.ReadFile(filepath.Join(libDir, "backup.conf")); err == nil {
			inc, exc, cats := backupRules(string(b))
			if len(inc)+len(exc)+len(cats) > 0 {
				c.Backup = &library.BackupConfig{Include: inc, Exclude: exc, Categories: cats}
				changed = true
			}
			del("backup.conf")
		}
	}
	// Show the full catalog in the file, keeping old exclusions off.
	if c.Backup != nil {
		if len(c.Backup.Categories) == 0 {
			c.Backup.Categories = defaultCategories()
			changed = true
		}
		var paths []string
		for _, x := range c.Backup.Exclude {
			if strings.HasPrefix(x, "@") {
				c.Backup.Categories[strings.TrimPrefix(x, "@")] = false
				changed = true
				continue
			}
			paths = append(paths, x)
		}
		c.Backup.Exclude = paths
	}

	if !learnSet(c) {
		var f learnIgnore
		if loadJSON(filepath.Join(libDir, "learn-ignore.json"), &f) == nil && len(f.IDs) > 0 {
			c.Learn = &library.LearnConfig{Ignore: f.IDs}
			changed = true
		}
		del("learn-ignore.json")
	}

	if !toolsSet(c) {
		if b, err := os.ReadFile(filepath.Join(libDir, "tools.json")); err == nil {
			var m struct {
				Tools []struct {
					Name, Version, Check, Install string
				} `json:"tools"`
			}
			if json.Unmarshal(b, &m) == nil && len(m.Tools) > 0 {
				tc := &library.ToolsConfig{}
				for _, t := range m.Tools {
					if t.Check == "" && t.Install == "" {
						tc.Pins = append(tc.Pins, library.ToolPin{Name: t.Name, Version: t.Version})
					} else {
						tc.Custom = append(tc.Custom, library.ToolCustom{Name: t.Name, Version: t.Version, Check: t.Check, Install: t.Install})
					}
				}
				c.Tools = tc
				changed = true
			}
			del("tools.json")
		}
	}

	// Uncommitted-work snapshots were dropped: their settings go.
	if c.Workspaces != nil {
		c.Workspaces = nil
		changed = true
	}
	if c.Backup != nil {
		if _, ok := c.Backup.Categories["wip"]; ok {
			delete(c.Backup.Categories, "wip")
			changed = true
		}
	}
	del("workspaces.json")

	if !usageSet(c) {
		if b, err := os.ReadFile(filepath.Join(libDir, "usage-sharing.json")); err == nil {
			var u struct {
				ShareWith []string `json:"share_usage_with"`
			}
			if json.Unmarshal(b, &u) == nil && len(u.ShareWith) > 0 {
				c.Usage = &library.UsageConfig{ShareWith: u.ShareWith}
				changed = true
			}
			del("usage-sharing.json")
		}
	}

	if !mcpSet(c) {
		if b, err := os.ReadFile(MCPConfigPath(home)); err == nil {
			var m mcpcfg.Config
			if toml.Unmarshal(b, &m) == nil && len(m.Servers) > 0 {
				c.MCP = &library.MCPConfig{Servers: map[string]library.MCPServer{}}
				for n, s := range m.Servers {
					c.MCP.Servers[n] = library.MCPServer{Command: s.Command, Args: s.Args, Env: s.Env, Agents: s.Agents}
				}
				changed = true
			}
			del("mcp.toml")
		}
	}
	return changed, nil
}

// backupRules converts the old line format into config.toml's
// include/exclude lists. Categories are on by default, so plain "@x" lines
// need no entry.
func backupRules(text string) (include, exclude []string, categories map[string]bool) {
	categories = map[string]bool{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "!@"):
			categories[strings.TrimPrefix(line, "!@")] = false
		case strings.HasPrefix(line, "@"):
			categories[strings.TrimPrefix(line, "@")] = true
		case strings.HasPrefix(line, "!"):
			exclude = append(exclude, line[1:])
		default:
			include = append(include, line)
		}
	}
	return
}

// LoadTools returns the tool manifest from config.toml [tools], or the old
// library/tools.json when the section is absent.
func LoadTools(home string) (*tools.Manifest, error) {
	if c, err := UserConfig(home); err == nil && toolsSet(&c) {
		m := &tools.Manifest{}
		for _, p := range c.Tools.Pins {
			m.Tools = append(m.Tools, tools.Tool{Name: p.Name, Version: p.Version})
		}
		for _, t := range c.Tools.Custom {
			m.Tools = append(m.Tools, tools.Tool{Name: t.Name, Version: t.Version, Check: t.Check, Install: t.Install})
		}
		return m, nil
	}
	return tools.Load(filepath.Join(LibraryDir(home), "tools.json"))
}

// SaveTools writes the manifest into config.toml [tools].
func SaveTools(home string, m *tools.Manifest) error {
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	tc := &library.ToolsConfig{}
	for _, t := range m.Tools {
		if t.Check == "" && t.Install == "" {
			tc.Pins = append(tc.Pins, library.ToolPin{Name: t.Name, Version: t.Version})
		} else {
			tc.Custom = append(tc.Custom, library.ToolCustom{Name: t.Name, Version: t.Version, Check: t.Check, Install: t.Install})
		}
	}
	c.Tools = tc
	if err := SaveUserConfig(home, c); err != nil {
		return err
	}
	os.Remove(filepath.Join(LibraryDir(home), "tools.json"))
	return nil
}

// AddResident keeps newly added skills in the agents' own skill lists: a
// skill the user just imported should show up there now, not only through
// skill search. With no resident list every skill is resident already.
// Returns the names added.
func AddResident(home string, names ...string) ([]string, error) {
	c, err := UserConfig(home)
	if err != nil || c.Skills == nil || len(c.Skills.Resident) == 0 || len(names) == 0 {
		return nil, err
	}
	have := map[string]bool{}
	for _, n := range c.Skills.Resident {
		have[n] = true
	}
	var added []string
	for _, n := range names {
		if n != "" && !have[n] {
			c.Skills.Resident, have[n] = append(c.Skills.Resident, n), true
			added = append(added, n)
		}
	}
	if len(added) == 0 {
		return nil, nil
	}
	return added, SaveUserConfig(home, c)
}
