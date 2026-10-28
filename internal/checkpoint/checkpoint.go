// Package checkpoint turns a synced session of any agent into an
// agent-neutral Markdown hand-off (design doc 6.3, layer C2), so work can
// continue in a different agent or when the original one cannot resume.
package checkpoint

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Turn is one user or assistant message.
type Turn struct {
	Role string // user | assistant
	Text string
}

// noise filters injected context that is not part of the conversation.
func noise(s string) bool {
	t := stripInjected(s)
	for _, p := range []string{"<environment_context", "<user_instructions", "<permissions", "# AGENTS.md", "<INSTRUCTIONS>", "<system-reminder", "<command-name>", "<local-command"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return t == ""
}

// stripInjected drops the tagged context blocks agents put before (or
// instead of) what the user typed: <environment_context>…</environment_context>,
// <app-context>, <external_…> and the like.
func stripInjected(s string) string {
	t := strings.TrimSpace(s)
	for strings.HasPrefix(t, "<") {
		end := strings.IndexAny(t, "> \n")
		if end < 2 {
			break
		}
		name := t[1:end]
		if !tagName(name) {
			break
		}
		close := strings.Index(t, "</"+name+">")
		if close < 0 {
			break
		}
		t = strings.TrimSpace(t[close+len(name)+3:])
	}
	return t
}

func tagName(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	// Agent context tags are snake or kebab case; plain HTML tags such as
	// <b> may be what the user typed.
	return strings.ContainsAny(s, "_-")
}

func textOf(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, x := range c {
			if m, ok := x.(map[string]any); ok {
				switch m["type"] {
				case "text", "input_text", "output_text":
					if s, ok := m["text"].(string); ok {
						parts = append(parts, s)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// Parse extracts the conversation from a session file of the given agent.
func Parse(agent string, data []byte) []Turn {
	var out []Turn
	add := func(role, text string) {
		if (role == "user" || role == "assistant") && !noise(text) {
			out = append(out, Turn{role, stripInjected(text)})
		}
	}
	if agent == "comate" {
		var doc struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
				Code    string `json:"code"`
			} `json:"messages"`
		}
		if json.Unmarshal(data, &doc) == nil {
			for _, m := range doc.Messages {
				t := textOf(m.Content)
				if m.Code != "" {
					t = strings.TrimSpace(t + "\n" + m.Code)
				}
				add(m.Role, t)
			}
		}
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch agent {
		case "codex": // {"type":"response_item","payload":{"type":"message","role":..,"content":[..]}}
			if p, ok := line["payload"].(map[string]any); ok && p["type"] == "message" {
				role, _ := p["role"].(string)
				add(role, textOf(p["content"]))
			}
		case "claude": // {"type":"user"|"assistant","message":{"role":..,"content":..}}
			if m, ok := line["message"].(map[string]any); ok {
				role, _ := m["role"].(string)
				add(role, textOf(m["content"]))
			}
		}
	}
	return out
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…(truncated)"
}

// Source describes where the session came from.
type Source struct {
	Agent, Path, Host, ID string
}

// Render writes the hand-off document with the first request and the
// last `last` turns.
func Render(src Source, turns []Turn, last int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Hand-off: continue an earlier %s session\n\n", src.Agent)
	fmt.Fprintf(&b, "- Source: %s on %s (`%s`, session %s)\n- Turns: %d\n\n", src.Agent, src.Host, src.Path, src.ID, len(turns))
	if len(turns) == 0 {
		b.WriteString("_No user or assistant messages were found in this session._\n")
		return b.String()
	}
	for _, t := range turns {
		if t.Role == "user" {
			fmt.Fprintf(&b, "## Original request\n\n%s\n\n", clip(t.Text, 3000))
			break
		}
	}
	start := 0
	if last > 0 && len(turns) > last {
		start = len(turns) - last
	}
	fmt.Fprintf(&b, "## Most recent turns (%d of %d)\n\n", len(turns)-start, len(turns))
	for _, t := range turns[start:] {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", strings.ToUpper(t.Role[:1])+t.Role[1:], clip(t.Text, 2000))
	}
	b.WriteString("## Instructions for the next agent\n\n" +
		"Continue the task above from where it stopped. Re-check the current state of files and " +
		"repositories before acting; the conversation may be out of date.\n")
	return b.String()
}
