package client

import (
	"bytes"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/crypto"
	"github.com/hearth-project/hearth-cli/internal/server"
	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

const pw = "correct horse battery"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t     *testing.T
	url   string
	srv   *server.Server
	clock *clock
	data  string
	cfg   server.Config // storage of srv
}

func newEnv(t *testing.T) *env {
	isolateAgentHomes(t)
	KDF = crypto.KDFParams{Time: 1, MemoryKiB: 19 * 1024, Threads: 1}
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	data := t.TempDir()
	dbURL, blobs := servertest.Backends(t)
	srv, err := server.New(server.Config{DataDir: data, Database: dbURL, Blobs: blobs, Now: c.now, TokenTTL: 3650 * 24 * time.Hour, AuthPerMin: 1000, Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv)
	t.Cleanup(func() { hs.Close(); srv.Close() })
	invite, err := srv.CreateInvite(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Register(t.TempDir(), hs.URL, "dev@example.com", []byte(pw), invite); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, url: hs.URL, srv: srv, clock: c, data: data, cfg: server.Config{DataDir: data, Database: dbURL, Blobs: blobs}}
}

// machine is one simulated host with its own hearth home and agent dirs.
type machine struct {
	*Engine
	codex string
}

func (en *env) machine(name string) *machine {
	home, codex := en.t.TempDir(), en.t.TempDir()
	if _, err := Login(home, en.url, "dev@example.com", []byte(pw), name); err != nil {
		en.t.Fatal(err)
	}
	e, err := NewEngine(home)
	if err != nil {
		en.t.Fatal(err)
	}
	e.Adapters = []adapter.Adapter{adapter.CodexAt(codex), adapter.ClaudeAt(en.t.TempDir())}
	e.ChunkSize = 1000
	e.Logf = en.t.Logf
	return &machine{Engine: e, codex: codex}
}

const rel = "sessions/2026/10/22/rollout-x.jsonl"

func (m *machine) path() string { return filepath.Join(m.codex, filepath.FromSlash(rel)) }

func (m *machine) appendLines(t *testing.T, n int, tag string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(m.path()), 0o700)
	f, err := os.OpenFile(m.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := 0; i < n; i++ {
		f.WriteString(`{"type":"event","tag":"` + tag + `","i":` + strings.Repeat("7", i%50) + "}\n")
	}
}

func (m *machine) push(t *testing.T) PushStats {
	t.Helper()
	st, err := m.PushOnce()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (m *machine) read(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(m.path())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (m *machine) only(t *testing.T) RemoteSession {
	t.Helper()
	all, err := m.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("want 1 session, got %d (%v)", len(all), err)
	}
	return all[0]
}

func TestSyncRestoreAndLease(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")

	a.appendLines(t, 100, "a1")
	if st := a.push(t); st.Bytes == 0 {
		t.Fatal("nothing uploaded")
	}
	a.appendLines(t, 20, "a2")
	a.push(t)
	if st := a.push(t); st.Bytes != 0 {
		t.Fatalf("idle push uploaded %d bytes", st.Bytes)
	}

	rs := b.only(t)
	if rs.Meta.Agent != "codex" || rs.Meta.RelPath != rel {
		t.Fatalf("bad meta %+v", rs.Meta)
	}
	if r, err := b.Restore(rs, false); err != nil || r.Action != "restored" {
		t.Fatalf("restore: %+v %v", r, err)
	}
	if !bytes.Equal(a.read(t), b.read(t)) {
		t.Fatal("restored file differs")
	}

	// a still holds the lease, so b's appends are refused until it takes over.
	b.appendLines(t, 5, "b1")
	if st, _ := b.PushOnce(); st.Bytes != 0 {
		t.Fatal("b uploaded while a holds the lease")
	}
	if err := b.Take(rs.ID); err != nil {
		t.Fatal(err)
	}
	if st := b.push(t); st.Bytes == 0 {
		t.Fatal("b did not upload after taking the lease")
	}
	a.appendLines(t, 1, "a3")
	if st, _ := a.PushOnce(); st.Bytes != 0 {
		t.Fatal("a uploaded after losing the lease")
	}

	// A third machine sees b's version.
	c := en.machine("c")
	if _, err := c.Restore(c.only(t), false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.read(t), c.read(t)) {
		t.Fatal("c does not match b")
	}
}

func TestRewriteStartsNewGeneration(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	a.appendLines(t, 50, "old")
	a.push(t)
	os.WriteFile(a.path(), []byte("{\"rewritten\":true}\n"), 0o600)
	a.push(t)
	rs := b.only(t)
	if rs.Generation != 2 {
		t.Fatalf("generation = %d, want 2", rs.Generation)
	}
	if _, err := b.Restore(rs, false); err != nil {
		t.Fatal(err)
	}
	if string(b.read(t)) != "{\"rewritten\":true}\n" {
		t.Fatalf("got %q", b.read(t))
	}
}

func TestCrashRecovery(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	a.appendLines(t, 80, "x")
	a.push(t)
	size := a.only(t).Size

	// Crash after upload but before state was saved: server is ahead.
	sid := a.only(t).ID
	fs := a.st.get(sid)
	fs.Uploaded = 0
	a.st.put(sid, fs)
	a.seen = map[string]fileStamp{}
	a.appendLines(t, 10, "y")
	a.push(t)
	got := a.only(t)
	if got.Size != int64(len(a.read(t))) || got.Size <= size {
		t.Fatalf("server size %d, local %d", got.Size, len(a.read(t)))
	}

	// Lost state entirely (fresh process, state.json gone): must verify and
	// resume rather than duplicate.
	os.Remove(filepath.Join(a.Home, "state.json"))
	e2, _ := NewEngine(a.Home)
	e2.Adapters, e2.ChunkSize, e2.Logf = a.Adapters, 1000, t.Logf
	a.Engine = e2
	a.appendLines(t, 3, "z")
	a.push(t)
	if a.only(t).Size != int64(len(a.read(t))) {
		t.Fatal("size mismatch after state loss")
	}
	b := en.machine("b")
	b.Restore(b.only(t), false)
	if !bytes.Equal(a.read(t), b.read(t)) {
		t.Fatal("restored content differs after crash recovery")
	}
}

// isolateAgentHomes points every agent home at temp dirs, so a test that
// falls back to the default render targets never writes the real ones.
func isolateAgentHomes(t *testing.T) {
	t.Helper()
	h := t.TempDir()
	for k, v := range map[string]string{
		"HOME": h, "CODEX_HOME": filepath.Join(h, ".codex"), "CLAUDE_CONFIG_DIR": filepath.Join(h, ".claude"),
		"HEARTH_COMATE_HOME": h, "HEARTH_WORKSPACE": "", "HEARTH_AGENTS": "", "HEARTH_HOME": "",
	} {
		t.Setenv(k, v)
	}
}

// A machine with an older copy of a conversation another machine continued
// must not overwrite the index (title, turns, last activity) with its own.
func TestStaleCopyKeepsNewerIndex(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	ask := func(m *machine, n int) {
		os.MkdirAll(filepath.Dir(m.path()), 0o700)
		f, err := os.OpenFile(m.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for i := 0; i < n; i++ {
			f.WriteString(`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"question"}]}}` + "\n")
		}
	}
	ask(a, 2)
	a.push(t)
	if _, err := b.Restore(b.only(t), false); err != nil {
		t.Fatal(err)
	}
	ask(a, 3)
	a.push(t)
	if got := b.only(t).Meta.Turns; got != 5 {
		t.Fatalf("server turns after a's push = %d, want 5", got)
	}
	// b re-indexes its stale copy (as after an index version change).
	sid := b.only(t).ID
	fs := b.st.get(sid)
	fs.IndexVer, fs.Scanned = 1, 0
	if err := b.st.put(sid, fs); err != nil {
		t.Fatal(err)
	}
	if _, err := b.PushOnce(); err != nil {
		t.Fatal(err)
	}
	if got := b.only(t).Meta.Turns; got != 5 {
		t.Fatalf("stale machine overwrote the index: turns = %d, want 5", got)
	}
}

// A machine whose daemon heartbeats is online for the others; signing off
// (graceful stop) makes it offline at once, and its next request online
// again.
func TestOnlineDevices(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	if err := a.API.Heartbeat(); err != nil {
		t.Fatal(err)
	}
	on := b.OnlineDevices()
	if on[a.Cfg.DeviceID] != "a" {
		t.Fatalf("a not online for b: %v", on)
	}
	if _, self := on[b.Cfg.DeviceID]; self {
		t.Fatal("b lists itself")
	}
	if err := a.API.SignOff(); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.OnlineDevices()[a.Cfg.DeviceID]; ok {
		t.Fatal("a still online after signing off")
	}
	// Restarted in the same second (self-update): online again at once.
	if err := a.API.Heartbeat(); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.OnlineDevices()[a.Cfg.DeviceID]; !ok {
		t.Fatal("a not online again after a heartbeat")
	}
	// Silent for longer than the window: offline.
	en.clock.add(16 * time.Minute)
	if _, ok := b.OnlineDevices()[a.Cfg.DeviceID]; ok {
		t.Fatal("a online after 16 minutes of silence")
	}
}
