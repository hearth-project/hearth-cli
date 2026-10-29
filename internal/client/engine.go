package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/crypto"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/mcpcfg"
	"github.com/hearth-project/hearth-cli/internal/pathmap"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// SessionMeta is encrypted with the vault key before upload.
type SessionMeta struct {
	Agent     string `json:"agent"`
	RelPath   string `json:"rel_path"`
	Host      string `json:"host"`
	OrigPath  string `json:"orig_path"`
	CreatedAt int64  `json:"created_at"`
	Kind      string `json:"kind,omitempty"` // session | memory
	// Thread is the Codex Desktop name and sidebar section, re-applied
	// after a restore. Updated whenever it changes locally.
	Thread *adapter.ThreadInfo `json:"thread,omitempty"`
	// Roots of the origin machine; other machines translate paths in the
	// transcript between them and their own (package pathmap).
	Roots pathmap.Roots `json:"roots,omitempty"`
	Cwd   string        `json:"cwd,omitempty"` // where the session ran (session scope)
	// Index of the conversation, kept with the metadata (encrypted):
	// title, number of turns, last write time and the machine that wrote it.
	Title        string `json:"title,omitempty"`
	Turns        int    `json:"turns,omitempty"`
	LastActive   int64  `json:"last_active,omitempty"`
	Writer       string `json:"writer,omitempty"`
	IndexVersion int    `json:"index_version,omitempty"`
	// Parent thread (fork origin or the thread that spawned a subagent).
	Parent     string `json:"parent,omitempty"`
	ParentKind string `json:"parent_kind,omitempty"`
	// Card: the memory card (goal, outcome, files, commands, errors).
	Card *MemoryCard `json:"card,omitempty"`
}

func cardPtr(c MemoryCard) *MemoryCard {
	if c.empty() {
		return nil
	}
	return &c
}

func hostname() string { h, _ := os.Hostname(); return h }

// localRoots are this machine's home and workspace.
func localRoots() pathmap.Roots {
	h, _ := os.UserHomeDir()
	return pathmap.Roots{Home: h, Workspace: paths.Workspace()}
}

// metaHash identifies what updateMeta last sent: the thread state and, for
// sessions that started on this machine inside its workspace, that
// workspace (so sessions uploaded before `hearth init` get their roots and
// cwd recorded once, and other machines can translate their paths).
func indexHash(s SessionStats, name string) string {
	return fmt.Sprintf("|title=%s|turns=%d|active=%d|writer=%s|iv=%d|parent=%s", name, s.Turns, s.LastActive, s.Writer, s.IndexVer, s.Parent)
}

func metaHash(f adapter.SessionFile, restored bool) string {
	h := threadHash(f.Thread)
	if ws := localRoots().Workspace; !restored && ws != "" {
		if cwd := sessionCwd(f); cwd != "" && under(cwd, []string{ws}) {
			h += "|ws=" + ws
		}
	}
	return h
}

// threadHash identifies the Thread metadata last sent to the server.
func threadHash(t *adapter.ThreadInfo) string {
	if t == nil {
		return ""
	}
	b, _ := json.Marshal(t)
	return string(b)
}

// Engine syncs local agent session files to the server.
type Engine struct {
	// Progress, when set, is told how far a large session download got
	// (done < 0: finished).
	Progress func(done, total int64)
	Home     string
	Cfg      *Config
	API      *API
	Vault    *crypto.Vault
	Adapters []adapter.Adapter
	// LibTargets are the agent directories the library is rendered into.
	LibTargets []library.Target
	// MCPTargets override where MCP servers are registered (nil = defaults).
	MCPTargets []mcpcfg.Target
	// NoPathMap restores transcripts byte for byte, without translating the
	// origin machine's paths ($HEARTH_NO_PATH_MAP=1).
	NoPathMap bool
	// Refresh forces git sources to be fetched even if recently fetched.
	Refresh   bool
	ChunkSize int
	LeaseTTL  time.Duration
	Logf      func(format string, args ...any)

	st               *state
	leases           map[string]time.Time // sid -> local lease expiry
	seen             map[string]fileStamp // sid -> last observed size/mtime
	blocked          map[string]string    // sid -> reason last logged (avoid log spam)
	warnedMassDelete bool                 // logged that many sessions vanished at once
	autoTargets      []library.Target     // LibTargets as derived from agents.json (not set by a caller)
	// knownWS: workspaces this user's machines recorded in session metadata
	// (filled by List); restores map them all to the local workspace.
	knownWS []string
	// quotaUntil pauses uploads after the server reported the quota full.
	quotaUntil time.Time
}

type fileStamp struct {
	size  int64
	mtime time.Time
}

// NewEngine loads login state from home.
func NewEngine(home string) (*Engine, error) {
	cfg, err := LoadConfig(home)
	if err != nil {
		return nil, err
	}
	v, err := LoadVault(home)
	if err != nil {
		return nil, err
	}
	st, err := loadState(home)
	if err != nil {
		return nil, err
	}
	hc, err := HTTPClient(home)
	if err != nil {
		return nil, err
	}
	targets := EnabledLibTargets(home)
	return &Engine{
		Home: home, Cfg: cfg, API: NewAPI(cfg.Server, cfg.Token, hc), Vault: v, Adapters: EnabledAdapters(home), LibTargets: targets, autoTargets: targets,
		ChunkSize: 4 << 20, LeaseTTL: 5 * time.Minute, Logf: log.Printf, NoPathMap: os.Getenv("HEARTH_NO_PATH_MAP") == "1",
		st: st, leases: map[string]time.Time{}, seen: map[string]fileStamp{}, blocked: map[string]string{},
	}, nil
}

// QuotaBackoff is how long uploads pause after a 507 (quota exceeded).
var QuotaBackoff = 10 * time.Minute

// PushStats summarises one push pass.
type PushStats struct {
	Files, Uploaded, Skipped int
	Foreign                  int // not this user's (outside the workspace on a shared account)
	Bytes                    int64
	Deleted                  int // deleted in the agent, marked expired on the server
	Unselected               int // not selected in the backup configuration
}

// PushOnce uploads every new byte of every local session file.
func (e *Engine) PushOnce() (PushStats, error) {
	var ps PushStats
	var errs []error
	// A CLI command (restore) may have recorded sessions since.
	if err := e.st.refresh(); err != nil {
		errs = append(errs, err)
	}
	roots := ownedRoots(e.Home)
	present := map[string]bool{} // sessions with a local file
	for _, a := range e.Adapters {
		files, err := a.Sessions()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Name(), err))
			continue
		}
		for _, f := range files {
			present[e.sidFor(f)] = true
			if !e.owns(f, roots) {
				ps.Foreign++
				continue
			}
			if !e.selected(f) {
				ps.Unselected++
				continue
			}
			ps.Files++
			n, err := e.pushFile(f)
			if err != nil {
				ps.Skipped++
				e.note(f, err.Error())
				continue
			}
			if n > 0 {
				ps.Uploaded++
				ps.Bytes += n
			}
		}
	}
	for _, in := range Integrations() {
		n, err := in.ExpireDeleted(e, present)
		if err != nil {
			errs = append(errs, err)
		} else {
			ps.Deleted += n
		}
	}
	return ps, errors.Join(errs...)
}

// note logs a per-file problem once until it changes.
func (e *Engine) note(f adapter.SessionFile, msg string) {
	sid := e.sidFor(f)
	if e.blocked[sid] != msg {
		e.blocked[sid] = msg
		e.Logf("skip %s: %s", f.Logical(), msg)
	}
}

var errSkip = errors.New("skipped")

var errQuotaPaused = errors.New("storage quota exceeded; uploads paused (hearth status)")

// sidFor returns the server session of a local file: restored files may
// live under a local name (an alias) different from the origin's.
func (e *Engine) sidFor(f adapter.SessionFile) string {
	if sid := e.st.alias(f.Logical()); sid != "" {
		return sid
	}
	sid := e.Vault.SessionID(f.Logical())
	if e.st.get(sid) == nil && e.st.remote(sid) == nil {
		if in, ok := Integration(f.Agent); ok {
			if moved := in.MovedSession(e, f.Logical()); moved != "" {
				_ = e.st.addAlias(f.Logical(), moved)
				return moved
			}
		}
	}
	return sid
}

// localLen is how many bytes of the local file the server has.
func (fs *fileState) localLen(ri *remoteInfo) int64 {
	if translated(ri) {
		return fs.Local
	}
	return fs.Uploaded
}

func translated(ri *remoteInfo) bool { return ri != nil && !ri.Map.Empty() }

func (e *Engine) pushFile(f adapter.SessionFile) (int64, error) {
	sid := e.sidFor(f)
	ri := e.st.remote(sid)
	fs := e.st.get(sid)
	if fs != nil && fs.Gone {
		return 0, nil // deleted on the server; use `session revive` to re-upload
	}
	if fs != nil && fs.Path != f.AbsPath {
		fs.Path = f.AbsPath
		if err := e.st.put(sid, fs); err != nil {
			return 0, err
		}
	}
	info, err := os.Stat(f.AbsPath)
	if err != nil {
		return 0, err
	}
	stamp := fileStamp{info.Size(), info.ModTime()}
	if fs != nil && e.seen[sid] == stamp && (fs.localLen(ri) == info.Size() || translated(ri)) {
		// Content unchanged; a thread may still have been renamed or moved.
		return 0, e.updateMeta(sid, f, fs)
	}
	file, err := os.Open(f.AbsPath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	if fs == nil {
		if fs, err = e.register(sid, f, file, info.Size(), ri); err != nil {
			return 0, err
		}
	} else if info.Size() < fs.localLen(ri) || !e.prefixUnchanged(file, fs, ri) {
		// The file was truncated or rewritten: start a new generation.
		if err := e.ensureLease(sid); err != nil {
			return 0, err
		}
		r, err := e.API.Reset(sid)
		if err != nil {
			return 0, e.serverErr(sid, fs, err)
		}
		e.Logf("%s was rewritten; re-uploading as generation %d", f.Logical(), r.Generation)
		fs = &fileState{Logical: fs.Logical, Gen: r.Generation, MetaHash: fs.MetaHash, CreatedAt: fs.CreatedAt, Path: f.AbsPath}
		if err := e.st.put(sid, fs); err != nil {
			return 0, err
		}
	}
	if fs.Gone {
		return 0, nil
	}
	if err := e.refreshStats(f, fs); err != nil {
		return 0, err
	}
	if err := e.updateMeta(sid, f, fs); err != nil {
		return 0, err
	}
	if info.Size() == fs.localLen(ri) {
		e.seen[sid] = stamp
		return 0, nil
	}
	if time.Now().Before(e.quotaUntil) {
		return 0, errQuotaPaused
	}
	if err := e.ensureLease(sid); err != nil {
		return 0, err
	}
	var n int64
	if translated(ri) {
		back := ri.Map.Inverse()
		if ri.BackSet {
			back = ri.Back
		}
		n, err = e.uploadTranslated(sid, fs, file, info.Size(), back)
	} else {
		n, err = e.upload(sid, fs, file, info.Size())
	}
	if err == nil {
		e.seen[sid] = stamp
		delete(e.blocked, sid)
	}
	return n, err
}

// register creates the session on the server. If the server already holds
// data (e.g. local state was lost), it verifies the server copy is a prefix
// of the local file before resuming from the server's size.
func (e *Engine) register(sid string, f adapter.SessionFile, file *os.File, size int64, ri *remoteInfo) (*fileState, error) {
	host, _ := os.Hostname()
	now := time.Now().Unix()
	st, err := scanStats(f.Agent, f.AbsPath, 0, SessionStats{IndexVer: sessionIndexVersion})
	if err != nil {
		return nil, err
	}
	st.Writer = host
	m := SessionMeta{Agent: f.Agent, RelPath: f.RelPath, Host: host, OrigPath: f.AbsPath, CreatedAt: now, Kind: f.Kind, Thread: f.Thread, Roots: localRoots(), Cwd: sessionCwd(f),
		Title: sessionTitle(threadName(f.Thread), st, f.RelPath), Turns: st.Turns, LastActive: st.LastActive, Writer: host, IndexVersion: sessionIndexVersion,
		Parent: st.Parent, ParentKind: st.ParentKind, Card: cardPtr(st.Card)}
	if ri != nil {
		// Restored from another machine: keep the origin's metadata.
		m = ri.Meta
		if f.Thread != nil {
			m.Thread = f.Thread
		}
		now = m.CreatedAt
	}
	meta, _ := json.Marshal(m)
	enc, err := e.Vault.SealMeta(sid, meta)
	if err != nil {
		return nil, err
	}
	r, err := e.API.PutSession(sid, enc)
	if StatusOf(err) == 410 {
		fs := &fileState{Logical: f.Logical(), Gone: true}
		e.Logf("%s was deleted on the server; not uploading (use `hearth session revive`)", f.Logical())
		return fs, e.st.put(sid, fs)
	}
	if err != nil {
		return nil, err
	}
	if f.Kind == adapter.KindMemory && !r.Pinned {
		// Long-term memory must not expire like conversations do.
		if _, err := e.API.Pin(sid, true); err != nil {
			return nil, err
		}
	}
	logical := f.Logical()
	if ri != nil {
		logical = ri.Meta.Agent + "/" + ri.Meta.RelPath
	}
	fs := &fileState{Logical: logical, Gen: r.Generation, CreatedAt: now, MetaHash: metaHash(f, ri != nil) + indexHash(st, m.Title), Path: f.AbsPath, SessionStats: st}
	if r.Size > 0 {
		if r.Size > size && !translated(ri) {
			return nil, fmt.Errorf("server copy (%d bytes) is longer than local file (%d bytes); run `hearth restore --force` or move the local file", r.Size, size)
		}
		remote, err := e.download(sid, r.Generation, r.Size)
		if err != nil {
			return nil, err
		}
		if translated(ri) {
			remote = ri.Map.Apply(remote)
		}
		if int64(len(remote)) > size {
			return nil, errors.New("local file is shorter than the server copy; run `hearth restore --force` or move the local file")
		}
		local := make([]byte, len(remote))
		if _, err := file.ReadAt(local, 0); err != nil && err != io.EOF {
			return nil, err
		}
		if string(local) != string(remote) {
			return nil, errors.New("local file diverges from the server copy; refusing to overwrite")
		}
		fs.Uploaded, fs.Local = r.Size, int64(len(remote))
		if fs.HeadHash, fs.TailHash, err = prefixHashes(file, fs.localLen(ri)); err != nil {
			return nil, err
		}
	}
	return fs, e.st.put(sid, fs)
}

func (e *Engine) prefixUnchanged(file *os.File, fs *fileState, ri *remoteInfo) bool {
	n := fs.localLen(ri)
	if n == 0 {
		return true
	}
	h, t, err := prefixHashes(file, n)
	return err == nil && h == fs.HeadHash && t == fs.TailHash
}

func (e *Engine) ensureLease(sid string) error {
	if time.Until(e.leases[sid]) > e.LeaseTTL/2 {
		return nil
	}
	_, err := e.API.Lease(sid, e.LeaseTTL, false)
	if StatusOf(err) == 423 {
		return errors.New("session is leased by another device (use `hearth session take`)")
	}
	if err != nil {
		return err
	}
	e.leases[sid] = time.Now().Add(e.LeaseTTL)
	return nil
}

// updateMeta re-sends the session metadata when the thread's name or
// section changed locally, or once to record the workspace of a session
// uploaded before this machine had one (see metaHash). Unknown local state (nil) never clears what the
// server has: a freshly restored machine may not have applied it yet.
func (e *Engine) updateMeta(sid string, f adapter.SessionFile, fs *fileState) error {
	host, _ := os.Hostname()
	title := sessionTitle(threadName(f.Thread), fs.SessionStats, f.RelPath)
	h := metaHash(f, e.st.remote(sid) != nil || fs.Restored) + indexHash(fs.SessionStats, title)
	if fs.Gone || h == fs.MetaHash {
		return nil
	}
	if fs.Uploaded > 0 || fs.Restored {
		// Another machine may have continued this conversation since our
		// copy: its title, turns and last activity are newer than ours.
		cur, err := e.API.Session(sid)
		if StatusOf(err) == 410 || StatusOf(err) == 404 {
			return nil
		}
		if err != nil {
			return err
		}
		if cur.Generation != fs.Gen || cur.Size > fs.Uploaded {
			fs.MetaHash = h // not ours to describe until we catch up
			return e.st.put(sid, fs)
		}
	}
	created := fs.CreatedAt
	if created == 0 {
		created = time.Now().Unix()
	}
	m := SessionMeta{Agent: f.Agent, RelPath: f.RelPath, Host: host, OrigPath: f.AbsPath, CreatedAt: created, Kind: f.Kind, Thread: f.Thread, Roots: localRoots(), Cwd: sessionCwd(f),
		Title: title, Turns: fs.Turns, LastActive: fs.LastActive, Writer: host, IndexVersion: sessionIndexVersion,
		Parent: fs.Parent, ParentKind: fs.ParentKind, Card: cardPtr(fs.Card)}
	if ri := e.st.remote(sid); ri != nil {
		m = ri.Meta // keep the origin's path, roots and host
		if f.Thread != nil {
			m.Thread = f.Thread
		}
		if title == "" || (fs.Title == "" && f.Thread == nil) {
			// No local name yet: keep the one recorded elsewhere.
			title = m.Title
		}
		m.Title, m.Turns, m.LastActive, m.Writer = title, fs.Turns, fs.LastActive, host
		m.IndexVersion, m.Parent, m.ParentKind = sessionIndexVersion, fs.Parent, fs.ParentKind
		if c := cardPtr(fs.Card); c != nil {
			m.Card = c
		}
	}
	meta, _ := json.Marshal(m)
	enc, err := e.Vault.SealMeta(sid, meta)
	if err != nil {
		return err
	}
	if _, err := e.API.PutSession(sid, enc); err != nil {
		if StatusOf(err) == 410 {
			return nil
		}
		return err
	}
	fs.MetaHash, fs.CreatedAt = h, created
	return e.st.put(sid, fs)
}

// refreshStats indexes bytes appended since the last pass.
func (e *Engine) refreshStats(f adapter.SessionFile, fs *fileState) error {
	info, err := os.Stat(f.AbsPath)
	if err != nil {
		return err
	}
	from, base := fs.Scanned, fs.SessionStats
	if fs.IndexVer != sessionIndexVersion {
		from, base = 0, SessionStats{}
	}
	if info.Size() <= from {
		return nil
	}
	st, err := scanStats(f.Agent, f.AbsPath, from, base)
	if err != nil {
		return err
	}
	st.Writer, _ = os.Hostname()
	st.IndexVer = sessionIndexVersion
	if st != fs.SessionStats {
		fs.SessionStats = st
		return e.st.put(e.sidFor(f), fs)
	}
	return nil
}

func threadName(t *adapter.ThreadInfo) string {
	if t == nil {
		return ""
	}
	return t.Name
}
