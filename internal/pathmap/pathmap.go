// Package pathmap translates absolute paths in agent transcripts between
// machines. A session keeps the paths of the machine that first uploaded it
// (its origin) on the server; a machine whose home or workspace lives
// elsewhere sees them rewritten to its own locations, and lines it appends
// are rewritten back before upload. Only whole roots are replaced, on path
// boundaries, so /root/ws never matches inside /root/ws2.
package pathmap

import (
	"bytes"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Roots are the locations that differ between machines.
type Roots struct {
	Home      string `json:"home,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}

// Pair replaces From with To.
type Pair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Map is an ordered set of replacements (longest From first).
type Map []Pair

// Empty reports whether the map changes nothing.
func (m Map) Empty() bool { return len(m) == 0 }

// Inverse maps To back to From.
func (m Map) Inverse() Map {
	out := make(Map, len(m))
	for i, p := range m {
		out[i] = Pair{From: p.To, To: p.From}
	}
	sortMap(out)
	return out
}

func sortMap(m Map) {
	sort.SliceStable(m, func(i, j int) bool { return len(m[i].From) > len(m[j].From) })
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// ClaudeDir is how Claude Code names a project directory for cwd p.
func ClaudeDir(p string) string { return nonAlnum.ReplaceAllString(p, "-") }

func clean(p string) string {
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	return strings.TrimRight(filepath.ToSlash(filepath.Clean(p)), "/")
}

// Build returns the map from origin to local paths ("" = nothing to do).
// The workspace comes first: it usually lives under home, and both may
// move independently. With claude, the encoded project directory names
// are mapped too (they appear in transcripts as paths under ~/.claude).
func Build(origin, local Roots, claude bool) Map {
	var m Map
	add := func(from, to string) {
		from, to = clean(from), clean(to)
		if from == "" || to == "" || from == to || from == "/" || to == "/" {
			return
		}
		for _, p := range m {
			if p.From == from {
				return
			}
		}
		m = append(m, Pair{from, to})
		if claude {
			m = append(m, Pair{ClaudeDir(from), ClaudeDir(to)})
		}
	}
	add(origin.Workspace, local.Workspace)
	add(origin.Home, local.Home)
	sortMap(m)
	return m
}

// WithWorkspaces adds pairs mapping other workspaces of the same user
// (from other machines) to local.Workspace. It covers sessions whose origin
// did not record a workspace (uploaded before `hearth init`) and lines that
// other machines appended. Homes and "/" are never mapped this way.
func (m Map) WithWorkspaces(others []string, origin, local Roots, claude bool) Map {
	to := clean(local.Workspace)
	if to == "" {
		return m
	}
	out := append(Map(nil), m...)
	have := map[string]bool{}
	for _, p := range out {
		have[p.From] = true
	}
	for _, w := range others {
		w = clean(w)
		if w == "" || w == to || w == "/" || w == clean(origin.Home) || w == clean(local.Home) || have[w] {
			continue
		}
		have[w] = true
		out = append(out, Pair{w, to})
		if claude {
			out = append(out, Pair{ClaudeDir(w), ClaudeDir(to)})
		}
	}
	sortMap(out)
	return out
}

// pathByte reports bytes that continue a path component.
func pathByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-'
}

// sentenceEnd reports punctuation after a path that ends a sentence
// ("in /root/ws. Now"), not a longer name ("/root/ws.bak").
func sentenceEnd(b []byte, i int) bool {
	return (b[i] == '.' || b[i] == ',') && (i+1 == len(b) || !pathByte(b[i+1]) && b[i+1] != '/')
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Apply rewrites every occurrence of a From on a path boundary. Absolute
// roots must not continue a path on either side; encoded Claude directory
// names may be followed by "-<subdir>" (a cwd below the root).
func (m Map) Apply(b []byte) []byte {
	if len(m) == 0 {
		return b
	}
	var out bytes.Buffer
	i := 0
	last := 0
	for i < len(b) {
		matched := false
		// A JSON escape (\n, \t, \r, \") before the path is a boundary too.
		if i == 0 || !pathByte(b[i-1]) || (i >= 2 && b[i-2] == '\\' && (b[i-1] == 'n' || b[i-1] == 't' || b[i-1] == 'r')) {
			for _, p := range m {
				f := p.From
				if len(b)-i < len(f) || string(b[i:i+len(f)]) != f {
					continue
				}
				abs := f[0] == '/'
				if abs && i > 0 && b[i-1] == '/' && !(i >= 3 && string(b[i-3:i]) == "://") {
					continue // inside a longer path: /x/root/ws (file:///root/ws is fine)
				}
				if end := i + len(f); end < len(b) && (abs && pathByte(b[end]) && !sentenceEnd(b, end) || !abs && isAlnum(b[end])) {
					continue // /root/ws2, -root-ws2
				}
				{
					out.Write(b[last:i])
					out.WriteString(p.To)
					i += len(f)
					last = i
					matched = true
					break
				}
			}
		}
		if !matched {
			i++
		}
	}
	if last == 0 {
		return b
	}
	out.Write(b[last:])
	return out.Bytes()
}

// ApplyString is Apply for strings.
func (m Map) ApplyString(s string) string { return string(m.Apply([]byte(s))) }

// ClaudeRel maps the project directory of a Claude Code transcript
// ("projects/<encoded cwd>/...") so Claude finds it under the local cwd.
func (m Map) ClaudeRel(rel string) string {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) < 3 || parts[0] != "projects" {
		return rel
	}
	dir := parts[1]
	for _, p := range m {
		if strings.Contains(p.From, "/") {
			continue // only the encoded pairs apply to directory names
		}
		if dir == p.From || strings.HasPrefix(dir, p.From+"-") {
			return "projects/" + p.To + dir[len(p.From):] + "/" + parts[2]
		}
	}
	return rel
}
