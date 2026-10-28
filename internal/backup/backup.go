// Package backup is the user's upload selection: a .gitignore-like file in
// the synced library that says what Hearth uploads, so every machine of the
// user follows the same habits. Lines select; "!" lines exclude.
//
//	# agents, refined per kind (most specific line wins)
//	@codex             everything of Codex
//	@claude.memory     only Claude Code memory notes
//	!@claude.sessions  never Claude Code conversations
//	@sessions          fallback for agents without a line
//	@memory
//	@workspace         personal files in the workspace (memory, settings)
//	@library           skills, agents, instructions
//	@credentials       SSH keys, git/cloud credentials found by `hearth learn`
//	!@credentials      never offer or upload credentials
//	# paths (~/ = home, ${WORKSPACE}/ or relative = workspace); dirs end in /
//	~/.ssh/config
//	${WORKSPACE}/.claude/memory/
//	# exclusions win over everything, including categories
//	!~/.ssh/id_*_old
//	!**/*.pem
//
// Everything is on unless a "!@" line turns it off. Patterns use * (within a path
// segment), ** (any depth) and ?; a trailing / matches everything below.
package backup

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Categories known to Hearth.
const (
	Sessions    = "sessions"
	Memory      = "memory"
	Workspace   = "workspace"
	Library     = "library"
	Credentials = "credentials"
)

// FileName is the selection file inside the synced library.
const FileName = "backup.conf"

// Agent is one supported agent and the kinds of data it syncs.
type Agent struct {
	Name  string
	Kinds []string // e.g. sessions, memory
}

// DefaultFor is the selection written for a new user: everything Hearth
// supports is uploaded (every agent and kind, workspace files, work
// snapshots, credentials). Users opt out with "!" lines.
func DefaultFor(agents []Agent) string {
	var b strings.Builder
	b.WriteString(`# Hearth backup selection: what this account uploads (end-to-end encrypted).
# File: <hearth home>/library/backup.conf, synced to all your machines.
# Edit with "hearth backup edit" or enable / disable / add / exclude;
# "hearth backup status" shows the result.
#
# Everything is uploaded unless a line turns it off:
#   "@name" uploads, "!@name" never uploads; the most specific line wins
#   ("!@claude.sessions" beats "@claude"). Agents added later default to on.
#
# Agents:
`)
	for _, a := range agents {
		b.WriteString("@" + a.Name + "\n")
		for _, k := range a.Kinds {
			b.WriteString("@" + a.Name + "." + k + "\n")
		}
	}
	b.WriteString(`#
# Other data:
@workspace         # personal files inside the workspace
@credentials       # SSH keys, git / cloud logins ("!@credentials" = never)
#
# Extra files and directories (~/ = home, ${WORKSPACE}/ = workspace,
# * ** ? wildcards, trailing / = whole directory):
# ~/.gitconfig
# ${WORKSPACE}/notes/
#
# Exclusions ("!") win over everything above:
!**/*.tmp
!**/.DS_Store
`)
	return b.String()
}

// Default is the selection for the built-in agents.
var Default = DefaultFor([]Agent{
	{"codex", []string{Sessions, Memory}},
	{"claude", []string{Sessions, Memory}},
	{"comate", []string{Sessions, Memory}},
})

// Rule is one pattern line.
type Rule struct {
	Pattern string // as written
	Exclude bool
	abs     string // expanded absolute pattern
	re      *regexp.Regexp
	dir     bool
}

// Config is a parsed selection.
type Config struct {
	Categories map[string]bool
	// Denied are categories turned off on purpose ("!@credentials"): unlike
	// a missing line, Hearth stops offering them (e.g. in `hearth learn`).
	Denied   map[string]bool
	Includes []Rule
	Excludes []Rule
	Lines    []string // original text, for rewriting
}

// Parse reads a selection; unknown @categories are kept but unused.
func Parse(text, home, workspace string) *Config {
	c := &Config{Categories: map[string]bool{}, Denied: map[string]bool{}}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		raw := sc.Text()
		c.Lines = append(c.Lines, raw)
		line := strings.TrimSpace(raw)
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "!@") {
			c.Denied[strings.ToLower(strings.TrimPrefix(line, "!@"))] = true
			continue
		}
		if strings.HasPrefix(line, "@") {
			c.Categories[strings.ToLower(strings.TrimPrefix(line, "@"))] = true
			continue
		}
		ex := strings.HasPrefix(line, "!")
		pat := strings.TrimSpace(strings.TrimPrefix(line, "!"))
		r, ok := compile(pat, home, workspace)
		if !ok {
			continue
		}
		r.Exclude = ex
		if ex {
			c.Excludes = append(c.Excludes, r)
		} else {
			c.Includes = append(c.Includes, r)
		}
	}
	return c
}

// expand turns a pattern into an absolute slash path pattern.
func expand(pat, home, workspace string) (string, bool) {
	switch {
	case pat == "~" || strings.HasPrefix(pat, "~/"):
		if home == "" {
			return "", false
		}
		pat = home + strings.TrimPrefix(pat, "~")
	case strings.HasPrefix(pat, "${HOME}"):
		if home == "" {
			return "", false
		}
		pat = home + strings.TrimPrefix(pat, "${HOME}")
	case strings.HasPrefix(pat, "${WORKSPACE}"):
		if workspace == "" {
			return "", false
		}
		pat = workspace + strings.TrimPrefix(pat, "${WORKSPACE}")
	case strings.HasPrefix(pat, "**"), strings.HasPrefix(pat, "/"):
	default: // relative: the workspace
		if workspace == "" {
			return "", false
		}
		pat = workspace + "/" + pat
	}
	return filepath.ToSlash(pat), true
}

func compile(pat, home, workspace string) (Rule, bool) {
	abs, ok := expand(pat, home, workspace)
	if !ok || abs == "" {
		return Rule{}, false
	}
	dir := strings.HasSuffix(abs, "/")
	abs = strings.TrimSuffix(abs, "/")
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(abs); i++ {
		switch ch := abs[i]; ch {
		case '*':
			if i+1 < len(abs) && abs[i+1] == '*' {
				i++
				if i+1 < len(abs) && abs[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("(?:/.*)?$") // a directory pattern covers its contents
	re, err := regexp.Compile(b.String())
	if err != nil {
		return Rule{}, false
	}
	return Rule{Pattern: pat, abs: abs, re: re, dir: dir}, true
}

func (r Rule) match(abs string) bool { return r.re.MatchString(filepath.ToSlash(abs)) }

// Decide resolves a file's selection from its keys, most specific first
// (e.g. "codex.sessions", "codex", "sessions"): the first key the user
// enabled or denied wins. decided is false when none is mentioned.
func (c *Config) Decide(keys ...string) (on, decided bool) {
	if c == nil {
		return false, false
	}
	for _, k := range keys {
		k = strings.ToLower(k)
		if c.Denied[k] {
			return false, true
		}
		if c.Categories[k] {
			return true, true
		}
	}
	return false, false
}

// Enabled reports whether a category is uploaded: on unless denied.
func (c *Config) Enabled(cat string) bool { return c == nil || !c.Denied[strings.ToLower(cat)] }

// Selected resolves a file's keys (most specific first); data nobody
// mentioned is uploaded.
func (c *Config) Selected(keys ...string) bool {
	on, decided := c.Decide(keys...)
	return on || !decided
}

// Undecided reports a category the user has neither enabled nor denied.
func (c *Config) Undecided(cat string) bool {
	return c == nil || (!c.Categories[cat] && !c.Denied[cat])
}

// Excluded reports whether an absolute path matches an exclusion.
func (c *Config) Excluded(abs string) bool {
	if c == nil {
		return false
	}
	for _, r := range c.Excludes {
		if r.match(abs) {
			return true
		}
	}
	return false
}

// Included reports whether an absolute path is selected by a path rule
// (and not excluded).
func (c *Config) Included(abs string) bool {
	if c == nil || c.Excluded(abs) {
		return false
	}
	for _, r := range c.Includes {
		if r.match(abs) {
			return true
		}
	}
	return false
}

// Roots returns the literal (wildcard-free) prefixes of the include rules:
// the files or directories to walk.
func (c *Config) Roots() []string {
	var out []string
	for _, r := range c.Includes {
		lit := r.abs
		if k := strings.IndexAny(lit, "*?["); k >= 0 {
			lit = lit[:k]
			if j := strings.LastIndex(lit, "/"); j > 0 {
				lit = lit[:j]
			} else {
				continue // pattern like **/x: no walkable root
			}
		}
		if lit != "" {
			out = append(out, lit)
		}
	}
	return out
}

// Load reads the selection at path; a missing file yields Default.
func Load(path, home, workspace string) (*Config, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Parse(Default, home, workspace), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return Parse(string(b), home, workspace), true, nil
}

// cache avoids re-parsing on every daemon pass.
var cache struct {
	sync.Mutex
	key string
	cfg *Config
}

// Cached loads path, re-parsing only when the file or roots change.
func Cached(path, home, workspace string) *Config {
	key := path + "|" + home + "|" + workspace
	if fi, err := os.Stat(path); err == nil {
		key += "|" + fi.ModTime().String() + "|" + strconv.FormatInt(fi.Size(), 10)
	}
	cache.Lock()
	defer cache.Unlock()
	if cache.cfg != nil && cache.key == key {
		return cache.cfg
	}
	cfg, _, err := Load(path, home, workspace)
	if err != nil {
		cfg = Parse(Default, home, workspace)
	}
	cache.key, cache.cfg = key, cfg
	return cfg
}

// AppendLine adds a line to the selection file (creating it from Default),
// unless an identical line exists.
func AppendLine(path, line string) (bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		b, err = []byte(Default), nil
	}
	if err != nil {
		return false, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return false, nil
		}
	}
	text := strings.TrimRight(string(b), "\n") + "\n" + line + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}

// SetCategory turns a category on or off in the file.
func SetCategory(path, cat string, on bool) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		b, err = []byte(Default), nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	found := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		bare := strings.TrimSpace(strings.TrimPrefix(t, "#"))
		if j := strings.Index(bare, " #"); j >= 0 {
			bare = strings.TrimSpace(bare[:j])
		}
		bare = strings.TrimPrefix(bare, "!")
		if strings.EqualFold(bare, "@"+cat) {
			if found {
				lines[i] = "" // duplicate: drop
				continue
			}
			found = true
			if on {
				lines[i] = "@" + cat
			} else {
				lines[i] = "!@" + cat
			}
		}
	}
	if !found {
		if on {
			lines = append(lines, "@"+cat)
		} else {
			lines = append(lines, "!@"+cat)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
