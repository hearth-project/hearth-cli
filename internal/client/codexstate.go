package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
)

// Codex Desktop's thread list lives in Codex's own state database, which
// Codex rebuilds from the restored rollout files. Names and sidebar
// sections are not in the rollouts, so after a restore they are re-applied
// through the Codex app-server protocol (the API Codex Desktop itself uses)
// instead of writing Codex's private database.

// CodexApplyStats summarises one apply pass.
type CodexApplyStats struct {
	Named, Sectioned, Unchanged, Missing int
	Indexed                              int // restored threads registered in Codex's thread list
}

// CodexBinary is the codex executable used to talk to the app-server.
var CodexBinary = "codex"

// appServer is a minimal JSON-RPC client for the Codex app-server, over
// stdio (`codex app-server`) or the running Desktop's control socket.
type appServer struct {
	t    rpcTransport
	mu   sync.Mutex
	next int
	// onNotify, when set, sees every notification read while waiting.
	onNotify func(method string, params json.RawMessage)
}

// rpcTransport carries one JSON-RPC message per write/read.
type rpcTransport interface {
	write([]byte) error
	read() ([]byte, error)
	close()
}

type stdioTransport struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func (t *stdioTransport) write(b []byte) error  { _, err := t.in.Write(append(b, '\n')); return err }
func (t *stdioTransport) read() ([]byte, error) { return t.out.ReadBytes('\n') }
func (t *stdioTransport) close() {
	t.in.Close()
	done := make(chan struct{})
	go func() { t.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.cmd.Process.Kill()
		<-done
	}
}

func startAppServer(ctx context.Context, codexHome string) (*appServer, error) {
	bin, err := exec.LookPath(CodexBinary)
	if err != nil {
		return nil, fmt.Errorf("codex not installed: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, "app-server")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	cmd.Dir = os.TempDir()
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return newAppServer(&stdioTransport{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20)})
}

func newAppServer(t rpcTransport) (*appServer, error) {
	s := &appServer{t: t}
	if err := s.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "hearth", "version": "1"}}, nil); err != nil {
		s.close()
		return nil, err
	}
	if err := s.send(map[string]any{"jsonrpc": "2.0", "method": "initialized"}); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *appServer) send(m any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return s.t.write(b)
}

// rpcMsg is any message from the server: a response or a notification.
type rpcMsg struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// next reads one message, handing notifications to onNotify.
func (s *appServer) recv() (rpcMsg, error) {
	line, err := s.t.read()
	if err != nil {
		return rpcMsg{}, fmt.Errorf("codex app-server: %w", err)
	}
	var m rpcMsg
	if json.Unmarshal(line, &m) == nil && m.ID == nil && m.Method != "" && s.onNotify != nil {
		s.onNotify(m.Method, m.Params)
	}
	return m, nil
}

// call sends one request and waits for its response, skipping notifications.
func (s *appServer) call(method string, params, result any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	id := s.next
	if err := s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		m, err := s.recv()
		if err != nil {
			return err
		}
		if m.ID == nil || *m.ID != id {
			continue
		}
		if m.Error != nil {
			return fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		if result != nil {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	}
}

func (s *appServer) close() { s.t.close() }

type appThread struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Section *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"section"`
}

// listThreads returns every thread Codex knows (active and archived, all
// sources).
func (s *appServer) listThreads() (map[string]appThread, error) {
	out := map[string]appThread{}
	for _, archived := range []bool{false, true} {
		cursor := ""
		for page := 0; page < 1000; page++ {
			params := map[string]any{"limit": 200, "archived": archived,
				"sourceKinds": []string{"cli", "vscode", "exec", "appServer", "subAgent", "subAgentReview",
					"subAgentCompact", "subAgentThreadSpawn", "subAgentOther", "unknown"}}
			if cursor != "" {
				params["cursor"] = cursor
			}
			var r struct {
				Data       []appThread `json:"data"`
				NextCursor *string     `json:"nextCursor"`
			}
			if err := s.call("thread/list", params, &r); err != nil {
				// Older/newer servers may reject a source kind: retry with defaults.
				delete(params, "sourceKinds")
				if err2 := s.call("thread/list", params, &r); err2 != nil {
					return nil, err
				}
			}
			for _, t := range r.Data {
				out[t.ID] = t
			}
			if r.NextCursor == nil || *r.NextCursor == "" {
				break
			}
			cursor = *r.NextCursor
		}
	}
	return out, nil
}

// ApplyCodexThreads sets the names and sidebar sections recorded on other
// machines for threads restored into codexHome. Threads that already match
// are left alone, so running it again is cheap and changes nothing.
func ApplyCodexThreads(codexHome string, want map[string]adapter.ThreadInfo, reindex ...string) (CodexApplyStats, error) {
	var st CodexApplyStats
	if len(want) == 0 && len(reindex) == 0 {
		return st, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s, err := startAppServer(ctx, codexHome)
	if err != nil {
		return st, err
	}
	defer s.close()
	return s.applyThreads(want, reindex)
}

func (s *appServer) applyThreads(want map[string]adapter.ThreadInfo, reindex []string) (CodexApplyStats, error) {
	var st CodexApplyStats
	// Codex Desktop lists threads from Codex's state database, which only
	// learns about rollouts written by others (and their new cwd) when a
	// thread is read: read every restored thread once.
	for _, id := range reindex {
		if err := s.call("thread/read", map[string]any{"threadId": id, "includeTurns": false}, nil); err == nil {
			st.Indexed++
		}
	}
	if len(want) == 0 {
		return st, nil
	}
	have, err := s.listThreads()
	if err != nil {
		return st, err
	}
	var sections struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	var errs []error
	if err := s.call("threadSection/list", map[string]any{}, &sections); err != nil {
		errs = append(errs, err)
	}
	byID, byName := map[string]bool{}, map[string]string{}
	for _, x := range sections.Data {
		byID[x.ID] = true
		if _, dup := byName[x.Name]; !dup {
			byName[x.Name] = x.ID
		}
	}
	sectionFor := func(info adapter.ThreadInfo) (string, error) {
		if byID[info.SectionID] {
			return info.SectionID, nil // built-in (Pinned) or created earlier with the same id
		}
		if id, ok := byName[info.Section]; ok {
			return id, nil
		}
		if info.Section == "" {
			return "", errors.New("unknown section")
		}
		var r struct {
			Section struct {
				ID string `json:"id"`
			} `json:"section"`
		}
		if err := s.call("threadSection/create", map[string]any{"name": info.Section}, &r); err != nil {
			return "", err
		}
		byID[r.Section.ID], byName[info.Section] = true, r.Section.ID
		return r.Section.ID, nil
	}
	for _, id := range adapter.SortedThreadIDs(want) {
		info := want[id]
		cur, ok := have[id]
		if !ok {
			st.Missing++
			continue
		}
		changed := false
		if info.Name != "" && info.Name != cur.Name {
			if err := s.call("thread/name/set", map[string]any{"threadId": id, "name": info.Name}, nil); err != nil {
				errs = append(errs, err)
			} else {
				st.Named++
				changed = true
			}
		}
		if info.SectionID != "" || info.Section != "" {
			sec, err := sectionFor(info)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("section %q: %w", info.Section, err))
			case cur.Section == nil || cur.Section.ID != sec:
				if err := s.call("thread/section/move", map[string]any{"threadId": id, "sectionId": sec}, nil); err != nil {
					errs = append(errs, err)
				} else {
					st.Sectioned++
					changed = true
				}
			}
		}
		if !changed {
			st.Unchanged++
		}
	}
	return st, errors.Join(errs...)
}

// CodexThreadsToApply collects the thread metadata of remote Codex sessions.
// Sessions come newest access first; for a thread with several rollout
// files the most recently synced metadata wins.
func CodexThreadsToApply(sessions []RemoteSession) map[string]adapter.ThreadInfo {
	want := map[string]adapter.ThreadInfo{}
	for _, rs := range sessions {
		if rs.MetaErr != nil || rs.Meta.Agent != "codex" || rs.Meta.Thread == nil {
			continue
		}
		if id := adapter.CodexThreadID(rs.Meta.RelPath); id != "" {
			if _, seen := want[id]; !seen {
				want[id] = *rs.Meta.Thread
			}
		}
	}
	return want
}

// ApplyAgentState re-applies agent UI state after a restore (Codex thread
// names and sections). It is a no-op when Codex is not enabled.
func (e *Engine) ApplyAgentState(sessions []RemoteSession, restoredPaths ...string) (CodexApplyStats, error) {
	seen := map[string]bool{}
	var ids []string
	for _, p := range restoredPaths {
		if id := adapter.CodexThreadID(p); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, a := range e.Adapters {
		if a.Name() == "codex" {
			return ApplyCodexThreads(a.Root(), CodexThreadsToApply(sessions), ids...)
		}
	}
	return CodexApplyStats{}, nil
}
