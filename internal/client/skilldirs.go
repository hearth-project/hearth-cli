package client

import (
	"os"
	"path/filepath"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// SkillDirs lists, per agent, the skill directories it loads besides its
// home's skills/ (the target hearth renders into): the user's workspace
// project dirs and the cross-agent ~/.agents/skills.
type SkillDirs map[string][]string

// AgentSkillDirs returns the extra skill dirs of each agent on this machine.
func AgentSkillDirs(home string) SkillDirs {
	h, _ := os.UserHomeDir()
	d := SkillDirs{
		"codex":  {filepath.Join(h, ".agents", "skills")},
		"claude": nil,
		"comate": nil,
	}
	if ws := WorkspaceOf(home); ws != "" || os.Getenv("HEARTH_WORKSPACE") != "" {
		if ws == "" {
			ws = os.Getenv("HEARTH_WORKSPACE")
		}
		d["codex"] = append(d["codex"], filepath.Join(ws, ".codex", "skills"), filepath.Join(ws, ".agents", "skills"))
		d["claude"] = append(d["claude"], filepath.Join(ws, ".claude", "skills"))
		d["comate"] = append(d["comate"], filepath.Join(ws, ".comate", "skills"))
	}
	return d
}

// Has returns where agent t already has a skill of this name that hearth
// did not put there.
func (d SkillDirs) Has(t library.Target, name string) string {
	for _, dir := range d[t.Name] {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err != nil {
			continue
		}
		return p
	}
	return ""
}
