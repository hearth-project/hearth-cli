// Package mcpcfg keeps MCP server registrations in one agent-neutral file
// (library/mcp.toml) and renders them into each agent: Comate's
// ~/.comate/mcp.json, Claude Code's ~/.claude.json, and Codex through its
// own `codex mcp add` CLI. Credentials are written as ${secret:NAME} and
// resolved from the encrypted vault at render time.
package mcpcfg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hearth-project/hearth-cli/internal/paths"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Server is one MCP server (stdio).
type Server struct {
	Command string            `toml:"command" json:"command"`
	Args    []string          `toml:"args,omitempty" json:"args,omitempty"`
	Env     map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	Agents  []string          `toml:"agents,omitempty" json:"-"` // default: all
}

// Config is library/mcp.toml.
type Config struct {
	Servers map[string]Server `toml:"servers"`
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)
var secretRefRE = regexp.MustCompile(`\$\{secret:([A-Za-z0-9][A-Za-z0-9._-]*)\}`)

// Load reads mcp.toml (missing file = empty config).
func Load(path string) (*Config, error) {
	c := &Config{Servers: map[string]Server{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	dec := toml.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return nil, err
	}
	if c.Servers == nil {
		c.Servers = map[string]Server{}
	}
	return c, c.Validate()
}

func (c *Config) Validate() error {
	for n, s := range c.Servers {
		if !nameRE.MatchString(n) || s.Command == "" {
			return fmt.Errorf("server %q: invalid name or missing command", n)
		}
		for _, a := range s.Agents {
			if a != "codex" && a != "claude" && a != "comate" {
				return fmt.Errorf("server %q: unknown agent %q", n, a)
			}
		}
	}
	return nil
}

// Save writes mcp.toml.
func (c *Config) Save(path string) error {
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// SecretLookup resolves a secret by name.
type SecretLookup func(name string) ([]byte, bool, error)

func expandHome(v string) string { return paths.Expand(v) }

// resolve returns the server with ~/ and ${secret:...} expanded.
func resolve(s Server, lookup SecretLookup) (Server, error) {
	var firstErr error
	sub := func(v string) string {
		v = expandHome(v)
		return secretRefRE.ReplaceAllStringFunc(v, func(m string) string {
			name := secretRefRE.FindStringSubmatch(m)[1]
			val, ok, err := lookup(name)
			if err == nil && !ok {
				err = fmt.Errorf("secret %q not found (hearth secret set %s)", name, name)
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
			return strings.TrimRight(string(val), "\n")
		})
	}
	out := Server{Command: sub(s.Command)}
	for _, a := range s.Args {
		out.Args = append(out.Args, sub(a))
	}
	if len(s.Env) > 0 {
		out.Env = map[string]string{}
		for k, v := range s.Env {
			out.Env[k] = sub(v)
		}
	}
	return out, firstErr
}

func (s Server) forAgent(agent string) bool {
	if len(s.Agents) == 0 {
		return true
	}
	for _, a := range s.Agents {
		if a == agent {
			return true
		}
	}
	return false
}

func hashOf(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// State remembers what was rendered (hashes only, no secret values).
type State map[string]string // "<agent>/<server>" -> hash

func LoadState(path string) State {
	s := State{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

func (s State) Save(path string) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(path, b, 0o600)
}

// Action reports one change.
type Action struct {
	Agent, Server, Op, Detail string
}

// Target describes where one agent keeps MCP registrations.
type Target struct {
	Agent    string
	JSONPath string // comate, claude: file with a top-level "mcpServers"
	CodexBin string // codex: CLI used to add/remove servers
}

// DefaultTargets for this machine.
func DefaultTargets() []Target {
	h, _ := os.UserHomeDir()
	comate := os.Getenv("HEARTH_COMATE_HOME")
	if comate == "" {
		comate = h
	}
	ts := []Target{{Agent: "comate", JSONPath: filepath.Join(comate, ".comate", "mcp.json")}}
	claude := filepath.Join(h, ".claude.json")
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		claude = filepath.Join(d, ".claude.json")
	}
	ts = append(ts, Target{Agent: "claude", JSONPath: claude})
	if bin, err := Look("codex"); err == nil {
		ts = append(ts, Target{Agent: "codex", CodexBin: bin})
	}
	return ts
}

// Look finds an agent CLI (the client sets it to its lookup, which also
// knows the CLIs bundled in desktop apps).
var Look = exec.LookPath

// Render applies cfg to every target. Servers that exist in a target but
// were not written by hearth are left alone unless adopt is set.
func Render(cfg *Config, targets []Target, lookup SecretLookup, st State, adopt bool) ([]Action, error) {
	var acts []Action
	names := make([]string, 0, len(cfg.Servers))
	for n := range cfg.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, t := range targets {
		want := map[string]Server{}
		for _, n := range names {
			s := cfg.Servers[n]
			if !s.forAgent(t.Agent) {
				continue
			}
			r, err := resolve(s, lookup)
			if err != nil {
				acts = append(acts, Action{t.Agent, n, "failed", err.Error()})
				continue
			}
			want[n] = r
		}
		var a []Action
		var err error
		if t.CodexBin != "" {
			a, err = renderCodex(t, want, st, adopt)
		} else {
			a, err = renderJSON(t, want, st, adopt)
		}
		acts = append(acts, a...)
		if err != nil {
			return acts, err
		}
	}
	return acts, nil
}

func renderJSON(t Target, want map[string]Server, st State, adopt bool) ([]Action, error) {
	doc := map[string]json.RawMessage{}
	mode := os.FileMode(0o600)
	if b, err := os.ReadFile(t.JSONPath); err == nil {
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", t.JSONPath, err)
		}
		if fi, err := os.Stat(t.JSONPath); err == nil {
			mode = fi.Mode().Perm()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := doc["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, fmt.Errorf("%s: mcpServers: %w", t.JSONPath, err)
		}
	}
	var acts []Action
	changed := false
	for n, s := range want {
		key := t.Agent + "/" + n
		h := hashOf(s)
		cur, exists := servers[n]
		var curS Server
		if exists {
			json.Unmarshal(cur, &curS)
		}
		switch {
		case exists && hashOf(curS) == h:
			st[key] = h
			acts = append(acts, Action{t.Agent, n, "unchanged", ""})
			continue
		case exists && st[key] == "" && !adopt:
			acts = append(acts, Action{t.Agent, n, "skip", "already configured differently in " + t.JSONPath + " (use --adopt to replace)"})
			continue
		}
		b, _ := json.Marshal(s)
		servers[n] = b
		st[key] = h
		changed = true
		op := "create"
		if exists {
			op = "update"
		}
		acts = append(acts, Action{t.Agent, n, op, t.JSONPath})
	}
	for key := range st {
		agent, n, _ := strings.Cut(key, "/")
		if agent != t.Agent {
			continue
		}
		if _, keep := want[n]; !keep {
			if _, exists := servers[n]; exists {
				delete(servers, n)
				changed = true
				acts = append(acts, Action{t.Agent, n, "remove", t.JSONPath})
			}
			delete(st, key)
		}
	}
	if !changed {
		return acts, nil
	}
	raw, _ := json.Marshal(servers)
	doc["mcpServers"] = raw
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return acts, err
	}
	if err := os.MkdirAll(filepath.Dir(t.JSONPath), 0o700); err != nil {
		return acts, err
	}
	tmp := t.JSONPath + ".hearth-tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), mode); err != nil {
		return acts, err
	}
	return acts, os.Rename(tmp, t.JSONPath)
}

func renderCodex(t Target, want map[string]Server, st State, adopt bool) ([]Action, error) {
	if len(want) == 0 && len(st) == 0 {
		return nil, nil
	}
	// codex refuses to run when $CODEX_HOME does not exist yet (new machine).
	if h := os.Getenv("CODEX_HOME"); h != "" {
		os.MkdirAll(h, 0o700)
	} else if home, err := os.UserHomeDir(); err == nil {
		os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	}
	run := func(args ...string) error {
		out, err := exec.Command(t.CodexBin, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("codex %s: %v: %s", args[1], err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	var acts []Action
	for n, s := range want {
		key := "codex/" + n
		h := hashOf(s)
		existsErr := exec.Command(t.CodexBin, "mcp", "get", n).Run()
		exists := existsErr == nil
		switch {
		case exists && st[key] == h:
			acts = append(acts, Action{"codex", n, "unchanged", ""})
			continue
		case exists && st[key] == "" && !adopt:
			acts = append(acts, Action{"codex", n, "skip", "already configured in codex (use --adopt to replace)"})
			continue
		}
		if exists {
			if err := run("mcp", "remove", n); err != nil {
				return acts, err
			}
		}
		args := []string{"mcp", "add"}
		for _, k := range sortedKeys(s.Env) {
			args = append(args, "--env", k+"="+s.Env[k])
		}
		args = append(append(append(args, n, "--"), s.Command), s.Args...)
		if err := run(args...); err != nil {
			return acts, err
		}
		st[key] = h
		op := "create"
		if exists {
			op = "update"
		}
		acts = append(acts, Action{"codex", n, op, "codex mcp add"})
	}
	for key := range st {
		agent, n, _ := strings.Cut(key, "/")
		if agent != "codex" {
			continue
		}
		if _, keep := want[n]; !keep {
			if exec.Command(t.CodexBin, "mcp", "get", n).Run() == nil {
				if err := run("mcp", "remove", n); err != nil {
					return acts, err
				}
				acts = append(acts, Action{"codex", n, "remove", "codex mcp remove"})
			}
			delete(st, key)
		}
	}
	return acts, nil
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

var credRE = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|access[_-]?key|pat)$`)

// ImportJSON reads an existing mcpServers file. Env values whose names
// look like credentials are returned in secrets and replaced by
// ${secret:NAME} references.
func ImportJSON(path string) (map[string]Server, map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		MCPServers map[string]Server `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, nil, err
	}
	secrets := map[string]string{}
	for n, s := range doc.MCPServers {
		for k, v := range s.Env {
			if credRE.MatchString(k) && v != "" && !secretRefRE.MatchString(v) {
				name := k
				if old, ok := secrets[name]; ok && old != v {
					name = n + "_" + k // same variable, different value in another server
				}
				secrets[name] = v
				s.Env[k] = "${secret:" + name + "}"
			}
		}
		doc.MCPServers[n] = s
	}
	return doc.MCPServers, secrets, nil
}
