package library

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/paths"
	"gopkg.in/yaml.v3"
)

// Library is a directory with the canonical layout:
//
//	skills/<name>/SKILL.md (+ any references/, scripts/ ...)
//	agents/<name>.md
//	instructions/AGENTS.md
type Library struct{ Root string }

// SkillsDir is where the single copy of the skills lives: skills.dir from
// config.toml when set, else <library>/skills.
func (l Library) SkillsDir() string {
	if c, err := l.LoadConfig(); err == nil && c.Skills != nil {
		if d := strings.TrimSpace(c.Skills.Dir); d != "" {
			return paths.Expand(d)
		}
	}
	return filepath.Join(l.Root, "skills")
}
func (l Library) AgentsDir() string        { return filepath.Join(l.Root, "agents") }
func (l Library) InstructionsFile() string { return filepath.Join(l.Root, "instructions", "AGENTS.md") }

// Skill is one skill directory.
type Skill struct {
	Name string
	Dir  string
}

// Contents is everything found in a library.
type Contents struct {
	Skills       []Skill
	Agents       []*Agent
	Instructions string // path to instructions/AGENTS.md, "" if absent
	// Resident lists the skills rendered into every agent. nil means all;
	// the rest stay available on demand through `hearth mcp`.
	Resident []string
	Problems []string
}

// ResidentFile optionally lists (one per line) the skills to keep resident.
func (l Library) ResidentFile() string { return filepath.Join(l.Root, "resident.txt") }

// Load reads the library, collecting (not failing on) per-item problems.
func (l Library) Load() (*Contents, error) {
	c := &Contents{}
	skills, problems, err := loadSkills(l.SkillsDir(), "skills/")
	if err != nil {
		return nil, err
	}
	c.Skills, c.Problems = skills, problems
	entries, err := os.ReadDir(l.AgentsDir())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(l.AgentsDir(), e.Name()))
		if err != nil {
			return nil, err
		}
		a, err := ParseAgent(data)
		if err != nil {
			c.Problems = append(c.Problems, fmt.Sprintf("agents/%s: %v", e.Name(), err))
			continue
		}
		if a.Name+".md" != e.Name() {
			c.Problems = append(c.Problems, fmt.Sprintf("agents/%s: name %q does not match file name", e.Name(), a.Name))
			continue
		}
		c.Agents = append(c.Agents, a)
	}
	if conf, err := l.LoadConfig(); err == nil && conf.Skills != nil && len(conf.Skills.Resident) > 0 {
		c.Resident = append([]string{}, conf.Skills.Resident...)
	} else if b, err := os.ReadFile(l.ResidentFile()); err == nil {
		c.Resident = []string{}
		for _, line := range strings.Split(string(b), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				c.Resident = append(c.Resident, line)
			}
		}
	}
	if _, err := os.Stat(l.InstructionsFile()); err == nil {
		c.Instructions = l.InstructionsFile()
	}
	sort.Slice(c.Skills, func(i, j int) bool { return c.Skills[i].Name < c.Skills[j].Name })
	sort.Slice(c.Agents, func(i, j int) bool { return c.Agents[i].Name < c.Agents[j].Name })
	return c, nil
}

// Target is one agent's native layout.
type Target struct {
	Name      string // "codex" | "claude"
	Root      string // $CODEX_HOME, ~/.claude, or <project>/.codex ...
	agentExt  string
	instrFile string
}

// CodexTarget renders into a Codex home (or a project's .codex directory).
func CodexTarget(root string) Target {
	return Target{Name: "codex", Root: root, agentExt: ".toml", instrFile: "AGENTS.md"}
}

// ClaudeTarget renders into a Claude Code config dir (or a project's .claude).
func ClaudeTarget(root string) Target {
	return Target{Name: "claude", Root: root, agentExt: ".md", instrFile: "CLAUDE.md"}
}

func (t Target) skillPath(name string) string { return filepath.Join(t.Root, "skills", name) }
func (t Target) agentPath(name string) string {
	return filepath.Join(t.Root, "agents", name+t.agentExt)
}
func (t Target) instrPath() string { return filepath.Join(t.Root, t.instrFile) }

// WithoutInstructions is t for Import without its AGENTS.md / CLAUDE.md
// (machine-specific notes are often kept there; imported only on request).
func (t Target) WithoutInstructions() Target { t.instrFile = ""; return t }

// ComateTarget renders skills into ~/.comate/skills. Comate's agent and
// instruction formats are not supported yet, so only skills are rendered.
func ComateTarget(root string) Target { return Target{Name: "comate", Root: root} }

// SkillsOnly reports whether a target only receives skills.
func (t Target) SkillsOnly() bool { return t.agentExt == "" }

// DefaultTargets uses $CODEX_HOME / $CLAUDE_CONFIG_DIR or their defaults.
func DefaultTargets() []Target {
	home, _ := os.UserHomeDir()
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	comate := os.Getenv("HEARTH_COMATE_HOME")
	if comate == "" {
		comate = home
	}
	return []Target{CodexTarget(codex), ClaudeTarget(claude), ComateTarget(filepath.Join(comate, ".comate"))}
}

const (
	codexHeader  = "# Generated by hearth from the library; edit the library copy and run `hearth lib sync`.\n"
	claudeHeader = "<!-- Generated by hearth from the library; edit the library copy and run `hearth lib sync`. -->\n"
)

// renderAgent produces the target-native agent file and lists fields that
// the target cannot represent.
func (t Target) renderAgent(a *Agent) ([]byte, []string, error) {
	switch t.Name {
	case "codex":
		b, err := RenderCodexAgent(a, codexHeader)
		return b, ignored("x-claude", a.Claude), err
	case "claude":
		b, err := RenderClaudeAgent(a, claudeHeader)
		return b, ignored("x-codex", a.Codex), err
	}
	return nil, nil, fmt.Errorf("unknown target %q", t.Name)
}

func ignored(prefix string, m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, prefix+"."+k)
	}
	sort.Strings(out)
	return out
}

// renderInstructions points the target's instructions file at the library.
func (t Target) renderInstructions(src string) []byte {
	if t.Name == "claude" {
		// Claude Code expands @path imports in CLAUDE.md.
		return []byte(claudeHeader + "@" + src + "\n")
	}
	return nil // codex: symlink AGENTS.md instead (see Render)
}

// LoadSkillsDir lists <dir>/<name>/SKILL.md skills, ignoring invalid ones.
func LoadSkillsDir(dir string) ([]Skill, error) {
	s, _, err := loadSkills(dir, "")
	return s, err
}

func loadSkills(dir, prefix string) ([]Skill, []string, error) {
	var skills []Skill
	var problems []string
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		d := filepath.Join(dir, e.Name())
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			continue
		}
		if !ValidName(e.Name()) {
			problems = append(problems, fmt.Sprintf("%s%s: invalid name", prefix, e.Name()))
			continue
		}
		if _, err := os.Stat(filepath.Join(d, "SKILL.md")); err != nil {
			problems = append(problems, fmt.Sprintf("%s%s: missing SKILL.md", prefix, e.Name()))
			continue
		}
		skills = append(skills, Skill{Name: e.Name(), Dir: d})
	}
	return skills, problems, nil
}

// SkillDescription extracts the description from SKILL.md front matter,
// tolerating front matter that is not strict YAML.
func SkillDescription(data []byte) string {
	fm, _, err := splitFrontMatter(data)
	if err != nil {
		return ""
	}
	var v struct {
		Description string `yaml:"description"`
	}
	if yaml.Unmarshal(fm, &v) == nil {
		return strings.TrimSpace(v.Description)
	}
	for _, line := range strings.Split(string(fm), "\n") {
		if d, ok := strings.CutPrefix(line, "description:"); ok {
			return strings.Trim(strings.TrimSpace(d), `"'`)
		}
	}
	return ""
}
