package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/checkpoint"
)

// SessionStats is the small searchable index kept next to a conversation:
// a title, its turn count and when it was last written. It is computed
// incrementally from appended bytes, so syncing a long transcript does not
// re-read it.
const sessionIndexVersion = 4 // bump when the counting rules change (3: injected context blocks are not titles; 4: parent thread)

type SessionStats struct {
	Turns      int    `json:"turns,omitempty"`
	Title      string `json:"title,omitempty"`
	LastActive int64  `json:"last_active,omitempty"`
	Writer     string `json:"writer,omitempty"`
	Scanned    int64  `json:"scanned,omitempty"`
	IndexVer   int    `json:"index_version,omitempty"`
	// Parent is the thread this one was forked from or spawned by (a
	// subagent); ParentKind says which. Lists fold such threads into
	// their parent.
	Parent     string `json:"parent,omitempty"`
	ParentKind string `json:"parent_kind,omitempty"`
}

// scanStats reads the complete lines of path starting at from (a line
// boundary) and updates turns/title/lastActive. It returns the offset after
// the last complete line so a partially written line is read next time.
func scanStats(agent, path string, from int64, st SessionStats) (SessionStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return st, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return st, err
	}
	if from > fi.Size() {
		from, st.Turns, st.Title, st.LastActive = 0, 0, "", 0
	}
	if agent == "comate" { // one JSON document, rewritten in place
		data, err := os.ReadFile(path)
		if err != nil {
			return st, err
		}
		turns := checkpoint.Parse(agent, data)
		st.Turns = countTurns(turns)
		if st.Title == "" {
			st.Title = firstUser(turns)
		}
		st.Scanned = int64(len(data))
		return st, nil
	}
	if _, err := f.Seek(from, 0); err != nil {
		return st, err
	}
	r := bufio.NewReaderSize(f, 1<<20)
	scanned := from
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			if scanned == 0 && agent == "codex" {
				st.Parent, st.ParentKind = codexParent(line)
			}
			scanned += int64(len(line))
			turns := checkpoint.Parse(agent, line)
			st.Turns += countTurns(turns)
			if st.Title == "" {
				st.Title = firstUser(turns)
			}
			var l struct {
				Timestamp string `json:"timestamp"`
			}
			if json.Unmarshal(bytes.TrimSpace(line), &l) == nil && l.Timestamp != "" {
				if t, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
					if t.Unix() > st.LastActive {
						st.LastActive = t.Unix()
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	st.Scanned = scanned
	return st, nil
}

// codexParent reads a rollout's session_meta line: the thread it was
// forked from ("fork") or the thread that spawned it ("subagent").
func codexParent(line []byte) (string, string) {
	var m struct {
		Type    string `json:"type"`
		Payload struct {
			ForkedFrom string          `json:"forked_from_id"`
			Source     json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &m) != nil || m.Type != "session_meta" {
		return "", ""
	}
	var src struct {
		SubAgent json.RawMessage `json:"subagent"`
	}
	if len(m.Payload.Source) > 0 && m.Payload.Source[0] == '{' && json.Unmarshal(m.Payload.Source, &src) == nil && len(src.SubAgent) > 0 {
		var spawn struct {
			ThreadSpawn struct {
				Parent string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		}
		if json.Unmarshal(src.SubAgent, &spawn) == nil && spawn.ThreadSpawn.Parent != "" {
			return spawn.ThreadSpawn.Parent, "subagent"
		}
	}
	if m.Payload.ForkedFrom != "" {
		return m.Payload.ForkedFrom, "fork"
	}
	return "", ""
}

// countTurns counts conversation rounds (user requests), not messages.
func countTurns(turns []checkpoint.Turn) (n int) {
	for _, t := range turns {
		if t.Role == "user" {
			n++
		}
	}
	return n
}

func firstUser(turns []checkpoint.Turn) string {
	for _, t := range turns {
		if t.Role != "user" {
			continue
		}
		text := t.Text
		// Codex Desktop puts attachments first and the request after this.
		if i := strings.Index(text, "## My request:"); i >= 0 {
			text = text[i+len("## My request:"):]
		}
		s := strings.Join(strings.Fields(text), " ")
		if r := []rune(s); len(r) > 120 {
			s = string(r[:120]) + "…"
		}
		return s
	}
	return ""
}

// sessionTitle is the name to show: an agent-side name, else the first
// request.
func sessionTitle(metaTitle string, st SessionStats, relPath string) string {
	if metaTitle != "" {
		return metaTitle
	}
	if st.Title != "" {
		return st.Title
	}
	return relPath
}
