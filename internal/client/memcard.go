package client

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/checkpoint"
)

// A memory card is the small, long-lived summary of a conversation that
// the memory index searches: what was asked, how it ended, which files
// and commands were involved and which errors came up. It is extracted
// while the session is indexed (no model calls) and travels, encrypted,
// in the session metadata, so every machine can index every conversation
// without downloading it.

// MemoryCard is the card of one conversation.
type MemoryCard struct {
	Goal    string `json:"goal,omitempty"`
	Asks    string `json:"asks,omitempty"` // later requests, newline separated (long conversations change topic)
	Outcome string `json:"outcome,omitempty"`
	Files   string `json:"files,omitempty"`  // newline separated, most recent last
	Cmds    string `json:"cmds,omitempty"`   // newline separated
	Errors  string `json:"errors,omitempty"` // newline separated
}

func (c MemoryCard) empty() bool { return c == MemoryCard{} }

const (
	cardGoal    = 400
	cardOutcome = 700
	cardFiles   = 20
	cardCmds    = 12
	cardErrors  = 6
	cardAsks    = 25
	cardItem    = 180
)

// pushItem appends v to a newline list, keeping it unique and the last n.
func pushItem(list, v string, n int) string {
	v = clipRunes(strings.Join(strings.Fields(v), " "), cardItem)
	if v == "" {
		return list
	}
	var items []string
	if list != "" {
		items = strings.Split(list, "\n")
	}
	out := items[:0]
	for _, x := range items {
		if x != v {
			out = append(out, x)
		}
	}
	out = append(out, v)
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return strings.Join(out, "\n")
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// cardTurns updates goal and outcome from parsed turns.
func cardTurns(c *MemoryCard, turns []checkpoint.Turn) {
	for _, t := range turns {
		text := strings.TrimSpace(t.Text)
		switch t.Role {
		case "user":
			if i := strings.Index(text, "## My request:"); i >= 0 {
				text = text[i+len("## My request:"):]
			}
			if c.Goal == "" {
				c.Goal = clipRunes(strings.Join(strings.Fields(text), " "), cardGoal)
			} else {
				c.Asks = pushItem(c.Asks, text, cardAsks)
			}
		case "assistant":
			if len([]rune(text)) >= 40 { // short acknowledgements are not outcomes
				c.Outcome = clipRunes(strings.Join(strings.Fields(text), " "), cardOutcome)
			}
		}
	}
}

var (
	patchFileRE = regexp.MustCompile(`(?m)^\*\*\* (?:Update|Add|Delete) File: (.+)$`)
	exitCodeRE  = regexp.MustCompile(`(?:exited with code|exit code|exit status) ([1-9][0-9]*)`)
	errorLineRE = regexp.MustCompile(`(?i)\b(error|exception|traceback|failed|failure|panic|fatal|denied|not found|timed out)\b`)
)

// firstErrorLine is the most telling line of a failed tool output.
func firstErrorLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && errorLineRE.MatchString(l) && !strings.HasPrefix(l, "Chunk ID") {
			return l
		}
	}
	return ""
}

// cardLine reads tool calls and their results from one transcript line.
func cardLine(agent string, line []byte, c *MemoryCard) {
	switch agent {
	case "codex":
		var l struct {
			Type    string `json:"type"`
			Payload struct {
				Type      string          `json:"type"`
				Name      string          `json:"name"`
				Arguments string          `json:"arguments"`
				Input     string          `json:"input"`
				Output    json.RawMessage `json:"output"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &l) != nil || l.Type != "response_item" {
			return
		}
		p := l.Payload
		switch p.Type {
		case "function_call":
			var a struct {
				Cmd     any    `json:"cmd"`
				Command any    `json:"command"`
				Input   string `json:"input"`
			}
			_ = json.Unmarshal([]byte(p.Arguments), &a)
			for _, v := range []any{a.Cmd, a.Command} {
				switch x := v.(type) {
				case string:
					c.Cmds = pushItem(c.Cmds, x, cardCmds)
				case []any:
					var parts []string
					for _, s := range x {
						if s, ok := s.(string); ok {
							parts = append(parts, s)
						}
					}
					// ["bash","-lc","..."]: the script is the interesting part.
					if n := len(parts); n >= 3 && (parts[n-2] == "-lc" || parts[n-2] == "-c") {
						parts = parts[n-1:]
					}
					c.Cmds = pushItem(c.Cmds, strings.Join(parts, " "), cardCmds)
				}
			}
			cardPatch(c, a.Input)
		case "custom_tool_call":
			cardPatch(c, p.Input)
		case "function_call_output", "custom_tool_call_output":
			var out string
			if json.Unmarshal(p.Output, &out) != nil {
				var o struct {
					Output  string `json:"output"`
					Content string `json:"content"`
				}
				_ = json.Unmarshal(p.Output, &o)
				out = o.Output + o.Content
			}
			if exitCodeRE.MatchString(out) {
				if e := firstErrorLine(out); e != "" {
					c.Errors = pushItem(c.Errors, e, cardErrors)
				}
			}
		}
	case "claude":
		var l struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &l) != nil || len(l.Message.Content) == 0 || l.Message.Content[0] != '[' {
			return
		}
		var parts []struct {
			Type    string          `json:"type"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			IsError bool            `json:"is_error"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(l.Message.Content, &parts) != nil {
			return
		}
		for _, p := range parts {
			switch p.Type {
			case "tool_use":
				var in struct {
					Command  string `json:"command"`
					FilePath string `json:"file_path"`
				}
				_ = json.Unmarshal(p.Input, &in)
				if in.Command != "" {
					c.Cmds = pushItem(c.Cmds, in.Command, cardCmds)
				}
				if in.FilePath != "" && (p.Name == "Edit" || p.Name == "Write" || p.Name == "MultiEdit" || p.Name == "NotebookEdit") {
					c.Files = pushItem(c.Files, in.FilePath, cardFiles)
				}
			case "tool_result":
				if !p.IsError {
					continue
				}
				var s string
				if json.Unmarshal(p.Content, &s) != nil {
					var blocks []struct {
						Text string `json:"text"`
					}
					_ = json.Unmarshal(p.Content, &blocks)
					for _, b := range blocks {
						s += b.Text + "\n"
					}
				}
				if e := firstErrorLine(s); e != "" {
					c.Errors = pushItem(c.Errors, e, cardErrors)
				} else if s = strings.TrimSpace(s); s != "" {
					c.Errors = pushItem(c.Errors, strings.SplitN(s, "\n", 2)[0], cardErrors)
				}
			}
		}
	}
}

func cardPatch(c *MemoryCard, patch string) {
	for _, m := range patchFileRE.FindAllStringSubmatch(patch, -1) {
		c.Files = pushItem(c.Files, strings.TrimSpace(m[1]), cardFiles)
	}
}
