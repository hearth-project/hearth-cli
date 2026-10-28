package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Codex ships its own importer for Claude Code sessions
// (externalAgentConfig/detect + externalAgentConfig/import): it converts a
// Claude transcript into a new Codex thread and keeps a ledger
// (external_agent_session_imports.json) so the same content is not imported
// twice. Hearth drives it rather than converting formats itself.

// wsTransport talks to the running Codex Desktop's app-server over its
// control socket (WebSocket on a unix socket). Imports made there are seen by
// the open window, unlike a separate `codex app-server` process.
type wsTransport struct {
	c   *websocket.Conn
	ctx context.Context
}

func (t *wsTransport) write(b []byte) error { return t.c.Write(t.ctx, websocket.MessageText, b) }
func (t *wsTransport) read() ([]byte, error) {
	_, b, err := t.c.Read(t.ctx)
	return b, err
}
func (t *wsTransport) close() { t.c.Close(websocket.StatusNormalClosure, "") }

// DesktopSocket is the control socket of a running Codex Desktop backend.
func DesktopSocket(codexHome string) string {
	return filepath.Join(codexHome, "app-server-control", "app-server-control.sock")
}

// dialDesktop connects to the running Desktop app-server, if any.
func dialDesktop(ctx context.Context, codexHome string) (*appServer, error) {
	sock := DesktopSocket(codexHome)
	if real, err := filepath.EvalSymlinks(sock); err == nil {
		sock = real
	}
	if fi, err := os.Stat(sock); err != nil || fi.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("no running Codex Desktop backend")
	}
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(64 << 20)
	return newAppServer(&wsTransport{c: c, ctx: ctx})
}

// CodexImport is the outcome for one Claude Code transcript.
type CodexImport struct {
	Source, Thread, Title, Error string
}

// claudeSessionInfo reads the cwd and first user prompt of a Claude Code
// transcript.
func claudeSessionInfo(path string) (cwd, title string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() && (cwd == "" || title == "") {
		var l struct {
			Type    string `json:"type"`
			Cwd     string `json:"cwd"`
			IsMeta  bool   `json:"isMeta"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if cwd == "" {
			cwd = l.Cwd
		}
		if title == "" && l.Type == "user" && !l.IsMeta {
			var s string
			var blocks []struct{ Type, Text string }
			if json.Unmarshal(l.Message.Content, &s) == nil {
				title = s
			} else if json.Unmarshal(l.Message.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type == "text" && b.Text != "" {
						title = b.Text
						break
					}
				}
			}
		}
	}
	if cwd == "" {
		return "", "", fmt.Errorf("%s: not a Claude Code transcript (no cwd)", path)
	}
	if r := []rune(strings.TrimSpace(title)); len(r) > 80 {
		title = string(r[:80])
	}
	return cwd, strings.TrimSpace(title), sc.Err()
}

type importSession struct {
	Path  string `json:"path"`
	Cwd   string `json:"cwd"`
	Title string `json:"title"`
}

type migrationItem struct {
	ItemType    string          `json:"itemType"`
	Description string          `json:"description"`
	Cwd         *string         `json:"cwd"`
	Details     json.RawMessage `json:"details"`
}

// ImportClaudeIntoCodex imports Claude Code transcripts into Codex as new
// threads. With no paths it imports every session Codex itself detects for
// cwds. It prefers the running Desktop backend (live reports whether it was
// used) so the open window learns about the new threads.
func ImportClaudeIntoCodex(codexHome string, paths, cwds []string) (res []CodexImport, live bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s, derr := dialDesktop(ctx, codexHome)
	if derr == nil {
		live = true
	} else if s, err = startAppServer(ctx, codexHome); err != nil {
		return nil, false, err
	}
	defer s.close()

	want := map[string]importSession{}
	for _, p := range paths {
		abs, _ := filepath.Abs(p)
		cwd, title, err := claudeSessionInfo(abs)
		if err != nil {
			res = append(res, CodexImport{Source: abs, Error: err.Error()})
			continue
		}
		want[abs] = importSession{Path: abs, Cwd: cwd, Title: title}
		cwds = append(cwds, cwd)
	}
	if len(paths) > 0 && len(want) == 0 {
		return res, live, nil
	}

	// Prefer Codex's own detection (it knows what its importer accepts).
	var det struct {
		Items []struct {
			ItemType string `json:"itemType"`
			Details  struct {
				Sessions []importSession `json:"sessions"`
			} `json:"details"`
		} `json:"items"`
	}
	if err := s.call("externalAgentConfig/detect", map[string]any{"includeHome": true, "cwds": dedup(cwds)}, &det); err != nil {
		return res, live, err
	}
	var sessions []importSession
	seen := map[string]bool{}
	for _, it := range det.Items {
		if it.ItemType != "SESSIONS" {
			continue
		}
		for _, d := range it.Details.Sessions {
			if _, ok := want[d.Path]; (len(paths) == 0 || ok) && !seen[d.Path] {
				seen[d.Path] = true
				sessions = append(sessions, d)
			}
		}
	}
	for p, w := range want { // not detected (e.g. outside ~/.claude): import as given
		if !seen[p] {
			sessions = append(sessions, w)
		}
	}
	if len(sessions) == 0 {
		return res, live, nil
	}
	details, _ := json.Marshal(map[string]any{"sessions": sessions, "plugins": []any{}, "skills": []any{},
		"mcpServers": []any{}, "hooks": []any{}, "subagents": []any{}, "commands": []any{}})
	item := migrationItem{ItemType: "SESSIONS", Description: "hearth: import Claude Code sessions", Details: details}

	type outcome struct {
		ItemTypeResults []struct {
			Successes []struct{ Source, Target, Title string } `json:"successes"`
			Failures  []map[string]any                         `json:"failures"`
		} `json:"itemTypeResults"`
		ImportID string `json:"importId"`
	}
	done := map[string]outcome{}
	s.onNotify = func(method string, params json.RawMessage) {
		if method == "externalAgentConfig/import/completed" {
			var o outcome
			if json.Unmarshal(params, &o) == nil {
				done[o.ImportID] = o
			}
		}
	}
	var started struct {
		ImportID string `json:"importId"`
	}
	if err := s.call("externalAgentConfig/import", map[string]any{"migrationItems": []migrationItem{item}}, &started); err != nil {
		return res, live, err
	}
	for {
		if o, ok := done[started.ImportID]; ok {
			for _, r := range o.ItemTypeResults {
				for _, ok := range r.Successes {
					res = append(res, CodexImport{Source: ok.Source, Thread: ok.Target, Title: ok.Title})
				}
				for _, f := range r.Failures {
					src, _ := f["source"].(string)
					b, _ := json.Marshal(f)
					res = append(res, CodexImport{Source: src, Error: string(b)})
				}
			}
			return res, live, nil
		}
		if _, err := s.recv(); err != nil {
			return res, live, fmt.Errorf("waiting for the import to finish: %w", err)
		}
	}
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// CodexHome returns the Codex adapter root, or "" when Codex is not enabled.
func (e *Engine) CodexHome() string {
	for _, a := range e.Adapters {
		if a.Name() == "codex" {
			return a.Root()
		}
	}
	return ""
}
