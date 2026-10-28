package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/pathmap"
)

// RemoteSession is a server session with decrypted metadata.
type RemoteSession struct {
	api.Session
	Meta    SessionMeta
	MetaErr error
}

// List returns all sessions on the server, newest access first.
func (e *Engine) List() ([]RemoteSession, error) {
	ss, err := e.API.Sessions()
	if err != nil {
		return nil, err
	}
	out := make([]RemoteSession, 0, len(ss))
	for _, s := range ss {
		rs := RemoteSession{Session: s}
		pt, err := e.Vault.OpenMeta(s.ID, s.EncMeta)
		if err == nil {
			err = json.Unmarshal(pt, &rs.Meta)
		}
		rs.MetaErr = err
		out = append(out, rs)
	}
	// Every workspace any of this user's machines recorded (workspace
	// files and sessions carry it).
	seen := map[string]bool{}
	e.knownWS = e.knownWS[:0]
	for _, rs := range out {
		if w := rs.Meta.Roots.Workspace; w != "" && !seen[w] {
			seen[w] = true
			e.knownWS = append(e.knownWS, w)
		}
	}
	return out, nil
}

// Resolve finds exactly one session whose ID starts with prefix.
func (e *Engine) Resolve(prefix string) (RemoteSession, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if len(prefix) < 4 {
		return RemoteSession{}, errors.New("session id prefix must have at least 4 characters")
	}
	all, err := e.List()
	if err != nil {
		return RemoteSession{}, err
	}
	var hit []RemoteSession
	for _, s := range all {
		if strings.HasPrefix(s.ID, prefix) {
			hit = append(hit, s)
		}
	}
	switch len(hit) {
	case 0:
		return RemoteSession{}, fmt.Errorf("no session matches %q", prefix)
	case 1:
		return hit[0], nil
	}
	return RemoteSession{}, fmt.Errorf("%d sessions match %q; use a longer prefix", len(hit), prefix)
}

// RestoreResult describes what Restore did with one session.
type RestoreResult struct {
	Path   string
	Action string // "restored", "appended", "up-to-date", "skipped"
	Reason string
}

// Restore writes a remote session back to its agent's native location.
// Existing local files are only extended when the server copy is a strict
// continuation of them, unless force is set.
func (e *Engine) Restore(rs RemoteSession, force bool) (RestoreResult, error) {
	if rs.MetaErr != nil {
		return RestoreResult{}, fmt.Errorf("cannot decrypt metadata: %w", rs.MetaErr)
	}
	var a adapter.Adapter
	for _, x := range e.Adapters {
		if x.Name() == rs.Meta.Agent {
			a = x
		}
	}
	if a == nil {
		return RestoreResult{Action: "skipped", Reason: "unknown agent " + rs.Meta.Agent}, nil
	}
	if a.Root() == "" {
		return RestoreResult{Action: "skipped", Reason: "no workspace on this machine (hearth init <dir>)"}, nil
	}
	// Paths of the origin machine become this machine's (home, workspace),
	// including the Claude project directory the transcript lives in.
	m, back := e.pathMapFor(rs.Meta)
	rel := rs.Meta.RelPath
	if rs.Meta.Agent == "claude" {
		rel = m.ClaudeRel(rel)
	}
	path, ok := a.NativePath(rel)
	if !ok {
		return RestoreResult{}, fmt.Errorf("refusing unsafe path %q", rel)
	}
	if in, ok := Integration(rs.Meta.Agent); ok {
		// Moved by the agent since (e.g. archived): update it where it is.
		if moved, ok := in.RestorePath(a.Root(), rel, path); ok {
			path, rel = moved, filepath.ToSlash(mustRel(a.Root(), moved))
		}
	}
	res := RestoreResult{Path: path}
	remote, err := e.download(rs.ID, rs.Generation, rs.Size)
	if err != nil {
		return res, err
	}
	raw := remote
	remote = m.Apply(remote)
	local, err := os.ReadFile(path)
	remapped := false
	switch {
	case errors.Is(err, os.ErrNotExist):
		res.Action = "restored"
	case err != nil:
		return res, err
	case string(local) == string(remote):
		res.Action = "up-to-date"
	case force:
		res.Action = "restored"
	case len(local) < len(remote) && string(remote[:len(local)]) == string(local):
		res.Action = "appended"
	case len(local) > len(remote) && string(local[:len(remote)]) == string(remote):
		// Continued here since: the daemon uploads the new turns.
		res.Action, res.Reason = "up-to-date", "(local copy has newer turns)"
	case e.restoredBefore(rs, raw, local):
		// Restored earlier with an older translation (e.g. before this
		// user's other workspaces were known): rewrite with the new paths.
		res.Action, remapped = "remapped", true
	default:
		res.Action, res.Reason = "skipped", "local file differs from server copy (use --force to overwrite)"
		return res, nil
	}
	if res.Action != "up-to-date" {
		if err := writeFileAtomic(path, remote, 0o600); err != nil {
			return res, err
		}
		// Codex orders its thread list by file mtime: keep the original
		// last-activity time instead of "now".
		mt := time.Now()
		if t, ok := adapter.LastTimestamp(remote); ok && t.Before(mt) {
			mt = t
		}
		if remapped {
			mt = mt.Add(time.Second) // a new mtime makes Codex re-read the thread (cwd)
		}
		_ = os.Chtimes(path, mt, mt)
	}
	// Record that the server already has every byte, so the daemon resumes
	// appending from here instead of re-uploading.
	f, err := os.Open(path)
	if err != nil {
		return res, err
	}
	defer f.Close()
	st := SessionStats{Turns: rs.Meta.Turns, Title: rs.Meta.Title, LastActive: rs.Meta.LastActive, Writer: rs.Meta.Writer, Scanned: int64(len(remote)), IndexVer: rs.Meta.IndexVersion,
		Parent: rs.Meta.Parent, ParentKind: rs.Meta.ParentKind}
	if st.IndexVer != sessionIndexVersion {
		if full, err := scanStats(rs.Meta.Agent, path, 0, SessionStats{IndexVer: sessionIndexVersion}); err == nil {
			st = full
		}
	}
	if fi, err := os.Stat(path); err == nil {
		st.Scanned = fi.Size() // local (possibly translated) length
	}
	fs := &fileState{Logical: rs.Meta.Agent + "/" + rs.Meta.RelPath, Gen: rs.Generation, Uploaded: rs.Size,
		Local: int64(len(remote)), MetaHash: threadHash(rs.Meta.Thread), CreatedAt: rs.Meta.CreatedAt, Restored: true, Path: path, SessionStats: st}
	if ws := rs.Meta.Roots.Workspace; ws != "" && rs.Meta.Cwd != "" && under(rs.Meta.Cwd, []string{ws}) && rs.Meta.Host == hostname() {
		fs.MetaHash += "|ws=" + ws // restoring our own session: nothing to re-send
	}
	// Keep the origin metadata even when no path translation is needed:
	// a machine without Codex thread names must not clear them on its next
	// push.
	ri := &remoteInfo{Map: m, Meta: rs.Meta, Back: back, BackSet: true}
	if err := e.st.setRemote(rs.ID, rs.Meta.Agent+"/"+rel, ri); err != nil {
		return res, err
	}
	if local := rs.Meta.Agent + "/" + rel; ri == nil && local != fs.Logical {
		// Same bytes under another name (e.g. archived by Codex here).
		if err := e.st.addAlias(local, rs.ID); err != nil {
			return res, err
		}
	}
	if fs.HeadHash, fs.TailHash, err = prefixHashes(f, fs.localLen(ri)); err != nil {
		return res, err
	}
	return res, e.st.put(rs.ID, fs)
}

// Take forcibly acquires the lease of a session, e.g. after the machine
// that held it was killed.
func (e *Engine) Take(sid string) error {
	if _, err := e.API.Lease(sid, e.LeaseTTL, true); err != nil {
		return err
	}
	e.leases[sid] = time.Now().Add(e.LeaseTTL)
	return nil
}

// Forget drops local sync state for a session (used after revive).
func (e *Engine) Forget(sid string) error { return e.st.put(sid, nil) }

// pathMapFor returns the translation of a session's paths to this machine.
// Only JSONL transcripts are translated (line-wise, see uploadTranslated);
// sessions from before roots were recorded are left as they are.
func (e *Engine) pathMapFor(meta SessionMeta) (restore, back pathmap.Map) {
	in, ok := Integration(meta.Agent)
	if e.NoPathMap || !ok || !in.Capabilities().PathMap || meta.Roots == (pathmap.Roots{}) || meta.Kind == adapter.KindMemory ||
		!strings.HasSuffix(meta.RelPath, ".jsonl") {
		return nil, nil
	}
	claude := in.Capabilities().ClaudePaths
	base := pathmap.Build(meta.Roots, localRoots(), claude)
	return base.WithWorkspaces(e.knownWS, meta.Roots, localRoots(), claude), base.Inverse()
}

// restoredBefore reports whether local is this machine's earlier restore
// of the session (same server bytes under the previous translation, plus
// at most what the server gained since), so it can be rewritten safely.
func (e *Engine) restoredBefore(rs RemoteSession, raw, local []byte) bool {
	fs := e.st.get(rs.ID)
	host, _ := os.Hostname()
	if fs == nil || fs.Gen != rs.Generation || !(fs.Restored || rs.Meta.Host != host) {
		return false
	}
	old := raw
	if ri := e.st.remote(rs.ID); ri != nil {
		old = ri.Map.Apply(raw)
	}
	return len(local) <= len(old) && string(old[:len(local)]) == string(local)
}

// codexMovedPath finds a Codex rollout that is not at want but was moved by
// archive/unarchive (same file name under archived_sessions/ or sessions/).
func codexMovedPath(root, rel, want string) (string, bool) {
	if _, err := os.Stat(want); err == nil {
		return "", false
	}
	base := filepath.Base(rel)
	for _, pat := range []string{filepath.Join(root, "archived_sessions", base), filepath.Join(root, "sessions", "*", "*", "*", base)} {
		if m, _ := filepath.Glob(pat); len(m) > 0 && m[0] != want {
			return m[0], true
		}
	}
	return "", false
}

func mustRel(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return r
}

// SessionData downloads a stored file's content.
func (e *Engine) SessionData(rs RemoteSession) ([]byte, error) {
	return e.download(rs.ID, rs.Generation, rs.Size)
}
