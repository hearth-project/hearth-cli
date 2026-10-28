package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Drives the real Codex importer when codex is installed.
func TestImportClaudeIntoCodex(t *testing.T) {
	if _, err := exec.LookPath(CodexBinary); err != nil {
		t.Skip("codex not installed")
	}
	dir := t.TempDir()
	home, codexHome, proj := filepath.Join(dir, "home"), filepath.Join(dir, "codex"), filepath.Join(dir, "proj")
	t.Setenv("HOME", home)
	enc := strings.ReplaceAll(proj, "/", "-")
	tdir := filepath.Join(home, ".claude", "projects", enc)
	for _, d := range []string{tdir, codexHome, proj} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	id := "11111111-2222-4333-8444-555555555555"
	path := filepath.Join(tdir, id+".jsonl")
	body := `{"type":"user","sessionId":"` + id + `","cwd":"` + proj + `","uuid":"a1","parentUuid":null,"timestamp":"2026-10-23T10:00:00.000Z","message":{"role":"user","content":"hello from claude"}}
{"type":"assistant","sessionId":"` + id + `","cwd":"` + proj + `","uuid":"a2","parentUuid":"a1","timestamp":"2026-10-23T10:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, live, err := ImportClaudeIntoCodex(codexHome, []string{path}, nil)
	if err != nil || live || len(res) != 1 || res[0].Error != "" || res[0].Thread == "" || res[0].Title != "hello from claude" {
		t.Fatalf("import: %+v live=%v err=%v", res, live, err)
	}
	threads, _ := filepath.Glob(filepath.Join(codexHome, "sessions", "*", "*", "*", "*"+res[0].Thread+".jsonl"))
	if len(threads) != 1 {
		t.Fatalf("no rollout for thread %s", res[0].Thread)
	}
	data, _ := os.ReadFile(threads[0])
	if !strings.Contains(string(data), "hi there") {
		t.Fatalf("assistant reply missing from rollout")
	}
	// Second run: Codex's ledger skips the unchanged transcript.
	res, _, err = ImportClaudeIntoCodex(codexHome, []string{path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Thread != "" {
			t.Fatalf("re-imported unchanged session: %+v", res)
		}
	}
	t.Logf("second run: %+v", res)
}
