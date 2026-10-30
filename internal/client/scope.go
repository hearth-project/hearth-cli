package client

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/pathmap"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// Session scope: on machines where several people share one Unix account,
// ~/.codex and ~/.claude hold everybody's conversations. With a workspace
// set (hearth init, or a machine profile), only sessions whose working
// directory is inside this user's workspace (or another directory they
// claimed with `hearth session scope add`) are uploaded. Without a
// workspace the home directory is assumed to be the user's own.

// ScopePath is the machine-local scope setting.
func ScopePath(home string) string { return filepath.Join(home, "scope.json") }

// Scope is the machine-local session scope.
type Scope struct {
	All   bool     `json:"all,omitempty"`   // this account is only mine: upload every session
	Paths []string `json:"paths,omitempty"` // more directories of mine (~, ${WORKSPACE} allowed)
}

// LoadScope reads the scope setting.
func LoadScope(home string) Scope {
	var s Scope
	loadJSON(ScopePath(home), &s)
	return s
}

// SaveScope writes the scope setting.
func SaveScope(home string, s Scope) error {
	b, err := jsonIndent(s)
	if err != nil {
		return err
	}
	return writeFileAtomic(ScopePath(home), b, 0o600)
}

// ownedRoots returns the directories whose sessions are this user's, or
// nil when every session is (no workspace, or scope "all").
func ownedRoots(home string) []string {
	s := LoadScope(home)
	ws := paths.Workspace()
	if s.All || ws == "" {
		return nil
	}
	roots := []string{ws}
	for _, p := range s.Paths {
		if x := expandWorkspace(p); filepath.IsAbs(x) {
			roots = append(roots, x)
		}
	}
	out := make([]string, 0, len(roots)*2)
	for _, r := range roots {
		r = filepath.Clean(r)
		out = append(out, r)
		if real, err := filepath.EvalSymlinks(r); err == nil && real != r {
			out = append(out, real)
		}
	}
	return out
}

func under(p string, roots []string) bool {
	p = filepath.Clean(p)
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) || r == "/" {
			return true
		}
	}
	return false
}

// cwdCache remembers the working directory of session files (it never
// changes for a transcript).
var cwdCache sync.Map // abs path -> string ("" = unknown)

// sessionCwd finds the working directory a session ran in.
func sessionCwd(f adapter.SessionFile) string {
	if v, ok := cwdCache.Load(f.AbsPath); ok {
		return v.(string)
	}
	cwd := ""
	if in, ok := Integration(f.Agent); ok {
		cwd = in.SessionCwd(f.AbsPath)
	}
	cwdCache.Store(f.AbsPath, cwd)
	return cwd
}

// wsCache remembers which workspace a directory is in for a minute
// (workspaces are rarely made, and a scan asks for every session).
var wsCache sync.Map // dir -> wsEntry

type wsEntry struct {
	ws string
	at time.Time
}

// otherWorkspace reports whether dir is inside a hearth workspace whose
// state is not home.
func otherWorkspace(dir, home string) bool {
	var ws string
	if v, ok := wsCache.Load(dir); ok && time.Since(v.(wsEntry).at) < time.Minute {
		ws = v.(wsEntry).ws
	} else {
		ws = FindWorkspace(dir)
		wsCache.Store(dir, wsEntry{ws, time.Now()})
	}
	return ws != "" && filepath.Clean(WorkspaceHome(ws)) != filepath.Clean(home)
}

// firstJSONField scans the first lines of a JSONL file for a value.
func firstJSONField(p string, get func(map[string]json.RawMessage) string) string {
	fh, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for i := 0; i < 20 && sc.Scan(); i++ {
		var m map[string]json.RawMessage
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			if v := get(m); v != "" {
				return v
			}
		}
	}
	return ""
}

// claudeProjectOwned matches Claude's encoded project directory
// (projects/<encoded cwd>/...) against the owned roots, for files without
// a cwd field (memory notes, empty transcripts).
func claudeProjectOwned(rel string, roots []string) bool {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) < 2 || parts[0] != "projects" {
		return false
	}
	for _, r := range roots {
		enc := pathmap.ClaudeDir(r)
		if parts[1] == enc || strings.HasPrefix(parts[1], enc+"-") {
			return true
		}
	}
	return false
}

// owns reports whether a local file is this user's to upload.
func (e *Engine) owns(f adapter.SessionFile, roots []string) bool {
	switch f.Agent {
	case "workspace", "files":
		return true // created from this user's workspace or chosen by them
	}
	if roots == nil {
		if LoadScope(e.Home).All {
			return true // the user said every session on this account is theirs
		}
		if fs := e.st.get(e.Vault.SessionID(f.Logical())); fs != nil && fs.Restored {
			return true
		}
		// No workspace: the home is assumed to be this user's, except
		// sessions run inside a workspace made with hearth init. That
		// workspace has its own login (on a shared account, most likely
		// another person's), and uploading its sessions from here too
		// would put them in this account or race its own daemon.
		cwd := sessionCwd(f)
		return cwd == "" || !otherWorkspace(cwd, e.Home)
	}
	// Restored from this account: it is ours wherever it ran.
	if sid := e.sidFor(f); e.st.alias(f.Logical()) != "" || e.st.remote(sid) != nil {
		return true
	}
	if fs := e.st.get(e.Vault.SessionID(f.Logical())); fs != nil && fs.Restored {
		return true
	}
	if cwd := sessionCwd(f); cwd != "" {
		return under(cwd, roots)
	}
	switch f.Agent {
	case "claude":
		return claudeProjectOwned(f.RelPath, roots)
	case "comate": // .comate/projects/<slug>/memory, slug = cwd without the leading "/"
		if rest, ok := strings.CutPrefix(f.RelPath, ".comate/projects/"); ok {
			slug, _, _ := strings.Cut(rest, "/")
			for _, r := range roots {
				enc := strings.TrimPrefix(pathmap.ClaudeDir(r), "-")
				if slug == enc || strings.HasPrefix(slug, enc+"-") {
					return true
				}
			}
		}
	}
	return false // unknown owner on a shared account: do not upload
}

// ForeignSession is a local session file that is not uploaded.
type ForeignSession struct {
	Agent, RelPath, Cwd string
}

// ForeignSessions lists local session files outside the scope.
func (e *Engine) ForeignSessions() []ForeignSession {
	roots := ownedRoots(e.Home)
	if roots == nil && LoadScope(e.Home).All {
		return nil
	}
	var out []ForeignSession
	for _, a := range e.Adapters {
		files, _ := a.Sessions()
		for _, f := range files {
			if !e.owns(f, roots) {
				out = append(out, ForeignSession{f.Agent, f.RelPath, sessionCwd(f)})
			}
		}
	}
	return out
}

// RemoteForeign is a session on the server that ran outside the scope of
// the machine that uploaded it (uploaded before scoping existed).
type RemoteForeign struct {
	RemoteSession
	Cwd string
}

// ForeignOnServer finds sessions on the server whose working directory is
// outside the workspace recorded by the uploading machine and outside the
// paths this account claimed (scope add / restored ones are not judged).
// Sessions without recorded roots or cwd are judged by content.
func (e *Engine) ForeignOnServer(extra []string) ([]RemoteForeign, error) {
	all, err := e.List()
	if err != nil {
		return nil, err
	}
	var out []RemoteForeign
	for _, rs := range all {
		m := rs.Meta
		if rs.MetaErr != nil || m.Roots.Workspace == "" || (m.Agent != "codex" && m.Agent != "claude") {
			continue // uploaded without a workspace: the whole home was the user's
		}
		roots := append([]string{m.Roots.Workspace}, extra...)
		cwd := m.Cwd
		if cwd == "" && m.Kind != adapter.KindMemory {
			data, err := e.download(rs.ID, rs.Generation, rs.Size)
			if err != nil {
				return nil, err
			}
			tmp, err := os.CreateTemp("", "hearth-cwd-*.jsonl")
			if err != nil {
				return nil, err
			}
			tmp.Write(data[:min(len(data), 1<<20)])
			tmp.Close()
			cwd = sessionCwd(adapter.SessionFile{Agent: m.Agent, AbsPath: tmp.Name()})
			os.Remove(tmp.Name())
		}
		owned := false
		switch {
		case cwd != "":
			owned = under(cwd, roots)
		case m.Agent == "claude":
			owned = claudeProjectOwned(m.RelPath, roots)
		}
		if !owned {
			out = append(out, RemoteForeign{rs, cwd})
		}
	}
	return out, nil
}
