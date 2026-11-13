package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

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

// KeyMemoryContext is the key memory an agent gets at session start: every
// fact in force under cwd (newest first, each cut to FactLimit, all within
// KeyMemoryLimit), with its id so the agent can rewrite or merge it.
func KeyMemoryContext(facts []memory.Doc, cwd string) string {
	var b strings.Builder
	used := 0
	for _, f := range facts {
		used += len(f.Body)
		if f.Cwd != "" && !strings.HasPrefix(cwd, f.Cwd) {
			continue // scoped to another directory
		}
		body := strings.Join(strings.Fields(f.Body), " ")
		if len(body) > FactLimit {
			body = cutUTF8(body, FactLimit) + "… (too long: tidy it, hearth memory tidy)"
		}
		line := "- [" + f.ID + "] " + body + "\n"
		if b.Len()+len(line) > KeyMemoryLimit+1024 {
			continue // an older entry that no longer fits; the next may
		}
		b.WriteString(line)
	}
	if b.Len() == 0 {
		return ""
	}
	s := "Key memory (Hearth: shared by all of this user's agents and machines; their decisions and verified rules, prefer it over other notes):\n" + b.String() +
		"Keep it current with the hearth MCP tool `remember`: add an entry when something is clearly worth keeping for future sessions " +
		"(a user decision or preference, a verified rule, a trap and how to avoid it), one line with the conclusion and where it holds; " +
		"pass `replaces` with the ids it supersedes when it corrects or merges entries; tell the user in one sentence. " +
		"Search earlier work of any machine or agent with `recall` (or `hearth recall <words>`)."
	if used*100 >= KeyMemoryLimit*85 {
		s += fmt.Sprintf(" Key memory is %d%% full: offer the user to tidy it (hearth memory tidy).", used*100/KeyMemoryLimit)
	}
	return s
}

func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Hook answers a SessionStart or UserPromptSubmit hook: the facts in
// force here at session start, the memories relevant to the prompt on each
// prompt. It returns "" when there is nothing worth adding. Budget: one
// index read, a few milliseconds.
func Hook(home string, in io.Reader) (string, error) {
	event, text, err := hookContext(home, in)
	if text == "" {
		return "", err
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": text}})
	return strings.TrimSpace(buf.String()), nil
}

// HookPlain answers the same hook as plain text, which Comate adds as
// context (it does not read hookSpecificOutput).
func HookPlain(home string, in io.Reader) (string, error) {
	_, text, err := hookContext(home, in)
	return text, err
}

func hookContext(home string, in io.Reader) (string, string, error) {
	var h HookInput
	b, _ := io.ReadAll(io.LimitReader(in, 1<<20))
	_ = json.Unmarshal(b, &h)
	if h.Cwd == "" {
		h.Cwd = os.Getenv("WORKSPACE_DIR") // Comate names the workspace there
	}
	if h.Cwd != "" && FoundHome(home) {
		home = HomeFor(h.Cwd) // the memories of the person working there
	}
	if !MemoryOn(home) {
		return "", "", nil
	}
	var text string
	switch h.Event {
	case "SessionStart":
		x, err := memory.OpenReadOnly(memory.Path(home))
		if err != nil {
			return "", "", nil
		}
		defer x.Close()
		facts, _ := x.List("fact", 1000)
		text = KeyMemoryContext(facts, h.Cwd)
		if text == "" {
			return "", "", nil
		}
	case "UserPromptSubmit", "":
		p := strings.TrimSpace(h.Prompt)
		if len([]rune(p)) < 6 || strings.HasPrefix(p, "/") {
			return "", "", nil
		}
		hits, err := Recall(home, p, h.Cwd, 8)
		if err != nil || len(hits) == 0 {
			return "", "", err
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
			return "", "", nil
		}
		text = "Possibly relevant memories from Hearth (earlier work on this user's machines and agents; verify before relying on them, details: hearth recall --show <id>):\n" +
			FormatHits(keep, 1500)
	default:
		return "", "", nil
	}
	event := h.Event
	if event == "" {
		event = "UserPromptSubmit"
	}
	return event, text, nil
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
