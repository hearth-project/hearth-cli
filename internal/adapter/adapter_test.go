package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func kinds(fs []SessionFile) map[string]string {
	m := map[string]string{}
	for _, f := range fs {
		m[f.RelPath] = f.Kind
	}
	return m
}

func TestAdapters(t *testing.T) {
	codex, claude, home, shared := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	touch(t, filepath.Join(codex, "sessions/2026/10/22/rollout-a.jsonl"))
	touch(t, filepath.Join(codex, "sessions/2026/10/22/notes.txt"))
	touch(t, filepath.Join(codex, "session_index.jsonl")) // outside sessions/: not synced
	touch(t, filepath.Join(claude, "projects/-root-ws/abc.jsonl"))
	touch(t, filepath.Join(claude, "projects/-root-ws/abc/subagents/agent-1.jsonl"))
	// Memory dir symlinked to a shared disk, as in the bootstrap case.
	touch(t, filepath.Join(shared, "claude-memory/MEMORY.md"))
	os.Symlink(filepath.Join(shared, "claude-memory"), filepath.Join(claude, "projects/-root-ws/memory"))
	touch(t, filepath.Join(home, ".comate-engine/store/chat_session_0da2"))
	touch(t, filepath.Join(home, ".comate-engine/db/embedding.db"))
	touch(t, filepath.Join(home, ".comate/projects/root-ws/memory/MEMORY.md"))
	touch(t, filepath.Join(home, ".comate/projects/root-ws/other.md"))

	cs, _ := CodexAt(codex).Sessions()
	if len(cs) != 1 || cs[0].Logical() != "codex/sessions/2026/10/22/rollout-a.jsonl" {
		t.Fatalf("codex: %+v", cs)
	}
	got := kinds(mustSessions(t, ClaudeAt(claude)))
	want := map[string]string{"projects/-root-ws/abc.jsonl": KindSession, "projects/-root-ws/abc/subagents/agent-1.jsonl": KindSession,
		"projects/-root-ws/memory/MEMORY.md": KindMemory}
	if len(got) != len(want) {
		t.Fatalf("claude: %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("claude %s: %q (all %v)", k, got[k], got)
		}
	}
	got = kinds(mustSessions(t, ComateAt(home)))
	if len(got) != 2 || got[".comate-engine/store/chat_session_0da2"] != KindSession || got[".comate/projects/root-ws/memory/MEMORY.md"] != KindMemory {
		t.Fatalf("comate: %v", got)
	}
	if none, err := CodexAt(filepath.Join(codex, "missing")).Sessions(); err != nil || len(none) != 0 {
		t.Fatalf("missing root: %v %v", none, err)
	}
	// Symlink loops terminate.
	os.Symlink(filepath.Join(claude, "projects"), filepath.Join(claude, "projects/-root-ws/loop"))
	mustSessions(t, ClaudeAt(claude))
}

func mustSessions(t *testing.T, a Adapter) []SessionFile {
	t.Helper()
	s, err := a.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNativePathRejectsEscapes(t *testing.T) {
	a := CodexAt("/home/u/.codex")
	if p, ok := a.NativePath("sessions/2026/x.jsonl"); !ok || p != "/home/u/.codex/sessions/2026/x.jsonl" {
		t.Fatalf("valid path rejected: %q", p)
	}
	for _, bad := range []string{"../../etc/passwd.jsonl", "/etc/x.jsonl", "sessions/../../x.jsonl", "config.toml", "sessions/x.sh", "auth.jsonl"} {
		if _, ok := a.NativePath(bad); ok {
			t.Errorf("unsafe path accepted: %q", bad)
		}
	}
	c := ComateAt("/home/u")
	if _, ok := c.NativePath(".comate/mcp.json"); ok {
		t.Error("comate config accepted as a session path")
	}
	if _, ok := c.NativePath(".comate/projects/x/memory/a.md"); !ok {
		t.Error("comate memory rejected")
	}
}
