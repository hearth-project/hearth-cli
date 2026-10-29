package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/memory"
)

// Recall searches the local memory index. It never touches the network,
// so it is safe on the agent's prompt path.
func Recall(home, query, cwd string, limit int) ([]memory.Hit, error) {
	x, err := memory.OpenReadOnly(memory.Path(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer x.Close()
	return x.Search(memory.Query{Text: query, Limit: limit, Cwd: cwd})
}

// MemoryDoc returns one memory by id.
func MemoryDoc(home, id string) (memory.Doc, bool) {
	x, err := memory.OpenReadOnly(memory.Path(home))
	if err != nil {
		return memory.Doc{}, false
	}
	defer x.Close()
	return x.Get(id)
}

// FormatHits renders hits for an agent's context, within max characters.
func FormatHits(hits []memory.Hit, max int) string {
	var b strings.Builder
	for i, h := range hits {
		when := time.Unix(h.At, 0).Format("2006-01-02")
		label := h.Kind
		if h.Agent != "" {
			label += " · " + h.Agent
		}
		if h.Machine != "" && h.Kind != "fact" {
			label += " · " + shortName(h.Machine)
		}
		body := strings.Join(strings.Fields(h.Body), " ")
		if r := []rune(body); len(r) > 360 {
			body = string(r[:360]) + "…"
		}
		line := fmt.Sprintf("%d. [%s · %s] %s", i+1, label, when, h.Title)
		if body != "" && body != h.Title {
			line += " — " + body
		}
		line += fmt.Sprintf(" (id %s)\n", h.ID)
		if b.Len()+len(line) > max && b.Len() > 0 {
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

func shortName(h string) string {
	if i := strings.IndexByte(h, '.'); i > 0 && strings.Count(h, ".") != 3 {
		return h[:i]
	}
	return h
}

// HookInput is what Codex and Claude Code send to a command hook on stdin.
type HookInput struct {
	Event     string `json:"hook_event_name"`
	Prompt    string `json:"prompt"`
	Cwd       string `json:"cwd"`
	SessionID string `json:"session_id"`
}

// Hook answers a SessionStart or UserPromptSubmit hook: the facts in
// force here at session start, the memories relevant to the prompt on each
// prompt. It returns "" when there is nothing worth adding. Budget: one
// index read, a few milliseconds.
func Hook(home string, in io.Reader) (string, error) {
	var h HookInput
	b, _ := io.ReadAll(io.LimitReader(in, 1<<20))
	_ = json.Unmarshal(b, &h)
	if !MemoryOn(home) {
		return "", nil
	}
	var text string
	switch h.Event {
	case "SessionStart":
		x, err := memory.OpenReadOnly(memory.Path(home))
		if err != nil {
			return "", nil
		}
		defer x.Close()
		facts, _ := x.List("fact", 40)
		var b strings.Builder
		for _, f := range facts {
			if f.Cwd != "" && !strings.HasPrefix(h.Cwd, f.Cwd) {
				continue // scoped to another directory
			}
			line := "- " + strings.Join(strings.Fields(f.Body), " ") + "\n"
			if b.Len()+len(line) > 1200 {
				break
			}
			b.WriteString(line)
		}
		if b.Len() == 0 {
			return "", nil
		}
		text = "What this user asked agents to remember (Hearth; confirmed by the user, prefer it over other notes):\n" + b.String() +
			"Search earlier work of any machine or agent with the hearth MCP tool `recall` (or `hearth recall <words>`)."
	case "UserPromptSubmit", "":
		p := strings.TrimSpace(h.Prompt)
		if len([]rune(p)) < 6 || strings.HasPrefix(p, "/") {
			return "", nil
		}
		hits, err := Recall(home, p, h.Cwd, 8)
		if err != nil || len(hits) == 0 {
			return "", err
		}
		var keep []memory.Hit
		for _, x := range hits {
			if h.SessionID != "" && strings.Contains(x.Ref, h.SessionID) {
				continue // this conversation itself
			}
			// Only clearly related memories: two shared terms (or one for a
			// one-word prompt), and not far below the best one.
			need := 2
			if len(strings.Fields(p)) == 1 && len([]rune(p)) < 12 {
				need = 1
			}
			if memory.Matched(p, x.Doc) < need || (len(keep) > 0 && x.Score < keep[0].Score*0.4) {
				continue
			}
			keep = append(keep, x)
			if len(keep) == 3 {
				break
			}
		}
		if len(keep) == 0 {
			return "", nil
		}
		text = "Possibly relevant memories from Hearth (earlier work on this user's machines and agents; verify before relying on them, details: hearth recall --show <id>):\n" +
			FormatHits(keep, 1500)
	default:
		return "", nil
	}
	event := h.Event
	if event == "" {
		event = "UserPromptSubmit"
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": text}})
	return strings.TrimSpace(buf.String()), nil
}

// MemoryIndexAge is when the index was last brought up to date.
func MemoryIndexAge(home string) (time.Time, map[string]int, int64) {
	p := memory.Path(home)
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}, nil, 0
	}
	x, err := memory.OpenReadOnly(p)
	if err != nil {
		return time.Time{}, nil, fi.Size()
	}
	defer x.Close()
	n, _ := x.Count()
	var at time.Time
	if v := x.Meta("indexed_at"); v != "" {
		var s int64
		fmt.Sscan(v, &s)
		at = time.Unix(s, 0)
	}
	size := fi.Size()
	if w, err := os.Stat(p + "-wal"); err == nil {
		size += w.Size()
	}
	return at, n, size
}
