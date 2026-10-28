// Package library implements the agent-neutral library of skills, agents
// and instructions (design doc section 7) and renders it into each coding
// agent's native layout.
package library

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// Agent is the canonical, agent-neutral definition stored as
// library/agents/<name>.md: YAML front matter plus the instructions body.
type Agent struct {
	Name         string
	Description  string
	Instructions string
	// Per-agent fields that have no neutral equivalent, kept verbatim and
	// only emitted for that agent (x-codex, x-claude in front matter).
	Codex  map[string]any
	Claude map[string]any
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidName reports whether s is safe as a skill/agent name and file name.
func ValidName(s string) bool { return nameRE.MatchString(s) && !strings.Contains(s, "..") }

type frontMatter struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Codex       map[string]any `yaml:"x-codex,omitempty"`
	Claude      map[string]any `yaml:"x-claude,omitempty"`
}

// splitFrontMatter separates "---\n<yaml>\n---\n<body>".
func splitFrontMatter(data []byte) (fm, body []byte, err error) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, nil, errors.New("missing front matter (file must start with ---)")
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		if strings.HasSuffix(rest, "\n---") {
			return []byte(rest[:len(rest)-4]), nil, nil
		}
		return nil, nil, errors.New("unterminated front matter")
	}
	return []byte(rest[:end]), []byte(strings.TrimPrefix(rest[end+5:], "\n")), nil
}

// ParseAgent parses the canonical AGENT.md format.
func ParseAgent(data []byte) (*Agent, error) {
	fmB, body, err := splitFrontMatter(data)
	if err != nil {
		return nil, err
	}
	var fm frontMatter
	if err := yaml.Unmarshal(fmB, &fm); err != nil {
		return nil, fmt.Errorf("front matter: %w", err)
	}
	if !ValidName(fm.Name) {
		return nil, fmt.Errorf("invalid agent name %q", fm.Name)
	}
	return &Agent{Name: fm.Name, Description: fm.Description, Instructions: string(body), Codex: fm.Codex, Claude: fm.Claude}, nil
}

// Format renders the canonical AGENT.md bytes.
func (a *Agent) Format() ([]byte, error) {
	fm, err := yaml.Marshal(frontMatter{Name: a.Name, Description: a.Description, Codex: a.Codex, Claude: a.Claude})
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	b.WriteString(ensureNL(a.Instructions))
	return b.Bytes(), nil
}

func ensureNL(s string) string {
	if s != "" && !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}

// ---- Codex: agents/<name>.toml with name, description, developer_instructions ----

// ParseCodexAgent converts a Codex agent TOML file.
func ParseCodexAgent(data []byte) (*Agent, error) {
	m := map[string]any{}
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	a := &Agent{}
	a.Name, _ = m["name"].(string)
	a.Description, _ = m["description"].(string)
	a.Instructions, _ = m["developer_instructions"].(string)
	delete(m, "name")
	delete(m, "description")
	delete(m, "developer_instructions")
	if len(m) > 0 {
		a.Codex = m
	}
	if !ValidName(a.Name) {
		return nil, fmt.Errorf("invalid agent name %q", a.Name)
	}
	return a, nil
}

// RenderCodexAgent produces a Codex agent TOML file. Instructions use a
// multi-line literal string when possible so the file stays readable.
func RenderCodexAgent(a *Agent, header string) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(header)
	enc := func(v any) (string, error) {
		out, err := toml.Marshal(map[string]any{"v": v})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(strings.TrimPrefix(string(out), "v = ")), nil
	}
	for _, kv := range [][2]string{{"name", a.Name}, {"description", a.Description}} {
		s, err := enc(kv[1])
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "%s = %s\n", kv[0], s)
	}
	if ins := a.Instructions; literalSafe(ins) {
		fmt.Fprintf(&b, "developer_instructions = '''\n%s'''\n", ensureNL(ins))
	} else {
		s, err := enc(ins)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "developer_instructions = %s\n", s)
	}
	if len(a.Codex) > 0 {
		keys := make([]string, 0, len(a.Codex))
		for k := range a.Codex {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		extra := map[string]any{}
		for _, k := range keys {
			extra[k] = a.Codex[k]
		}
		out, err := toml.Marshal(extra)
		if err != nil {
			return nil, err
		}
		b.Write(out)
	}
	// Round-trip check: never write a file Codex could not parse.
	if _, err := ParseCodexAgent(bytes.TrimPrefix(b.Bytes(), []byte(header))); err != nil {
		return nil, fmt.Errorf("rendered TOML does not parse: %w", err)
	}
	return b.Bytes(), nil
}

// literalSafe reports whether s can be a TOML multi-line literal string.
func literalSafe(s string) bool {
	if strings.Contains(s, "'''") || strings.HasSuffix(strings.TrimRight(s, "\n"), "'") {
		return false
	}
	for _, r := range s {
		if (r < 0x20 && r != '\t' && r != '\n') || r == 0x7f {
			return false
		}
	}
	return true
}

// ---- Claude Code: agents/<name>.md with front matter name, description, tools, model ... ----

// ParseClaudeAgent converts a Claude Code subagent file.
func ParseClaudeAgent(data []byte) (*Agent, error) {
	fmB, body, err := splitFrontMatter(data)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := yaml.Unmarshal(fmB, &m); err != nil {
		return nil, err
	}
	a := &Agent{Instructions: string(body)}
	a.Name, _ = m["name"].(string)
	a.Description, _ = m["description"].(string)
	delete(m, "name")
	delete(m, "description")
	if len(m) > 0 {
		a.Claude = m
	}
	if !ValidName(a.Name) {
		return nil, fmt.Errorf("invalid agent name %q", a.Name)
	}
	return a, nil
}

// RenderClaudeAgent produces a Claude Code subagent markdown file.
func RenderClaudeAgent(a *Agent, header string) ([]byte, error) {
	m := map[string]any{"name": a.Name, "description": a.Description}
	for k, v := range a.Claude {
		m[k] = v
	}
	fm, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	b.WriteString(header)
	b.WriteString(ensureNL(a.Instructions))
	return b.Bytes(), nil
}
