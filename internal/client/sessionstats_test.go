package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/checkpoint"
)

func TestScanStatsIncremental(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	lines := `{"timestamp":"2026-10-22T08:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first request"}]}}
{"timestamp":"2026-10-22T08:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"reply"}]}}
`
	os.WriteFile(p, []byte(lines), 0o600)
	st, err := scanStats("codex", p, 0, SessionStats{})
	if err != nil || st.Turns != 1 || st.Title != "first request" || st.LastActive == 0 {
		t.Fatalf("full scan: %+v %v", st, err)
	}
	// A partial trailing line is not counted until it is complete.
	os.WriteFile(p, append([]byte(lines), []byte(`{"timestamp":"2026-10-22T08:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"sec`)...), 0o600)
	st2, _ := scanStats("codex", p, st.Scanned, st)
	if st2.Turns != 1 {
		t.Fatalf("partial line counted: %+v", st2)
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`ond"}]}}` + "\n")
	f.Close()
	st3, _ := scanStats("codex", p, st2.Scanned, st2)
	if st3.Turns != 2 || st3.Title != "first request" {
		t.Fatalf("incremental scan: %+v", st3)
	}
}

func TestScanStatsClaude(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(p, []byte(`{"type":"user","timestamp":"2026-10-22T08:00:00Z","message":{"role":"user","content":"hello claude"}}
{"type":"assistant","timestamp":"2026-10-22T08:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
`), 0o600)
	st, err := scanStats("claude", p, 0, SessionStats{})
	if err != nil || st.Turns != 1 || st.Title != "hello claude" || st.LastActive == 0 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestFirstUserSkipsAttachmentPreamble(t *testing.T) {
	turns := []checkpoint.Turn{{Role: "user", Text: "# Files mentioned by the user:\n\n## a.md: /x/a.md\n\n## My request:\nfix the login bug"}}
	if got := firstUser(turns); got != "fix the login bug" {
		t.Fatalf("firstUser = %q", got)
	}
}
