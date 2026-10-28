package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/pathmap"
)

// A session started on machine A (home /a, workspace /a/ws) continues on B
// (home /b, workspace /b/work) and is read on C: every machine sees its own
// paths, the server keeps A's, Claude transcripts land in the project
// directory of the local cwd, and A still gets its own bytes back exactly.
func TestPathTranslationAcrossMachines(t *testing.T) {
	en := newEnv(t)
	type host struct {
		m        *machine
		home, ws string
	}
	mk := func(name string) host {
		m := en.machine(name)
		home := t.TempDir()
		return host{m, home, filepath.Join(home, "ws-"+name)}
	}
	A, B, C := mk("a"), mk("b"), mk("c")
	on := func(h host) {
		t.Setenv("HOME", h.home)
		t.Setenv("HEARTH_WORKSPACE", h.ws)
	}
	claudeRoot := func(h host) string { return h.m.Adapters[1].Root() }
	codexRel := "sessions/2026/10/23/rollout-2026-10-23T10-00-00-01a16500-0000-7000-8000-000000000001.jsonl"
	line := func(h host, s string) string {
		return `{"type":"turn_context","payload":{"cwd":"` + h.ws + `/repo","note":"` + s + `","file":"` + h.home + `/.bashrc"}}` + "\n"
	}

	// Machine A writes a Codex rollout and a Claude transcript (cwd ws/repo).
	on(A)
	codexA := filepath.Join(A.m.codex, filepath.FromSlash(codexRel))
	os.MkdirAll(filepath.Dir(codexA), 0o700)
	os.WriteFile(codexA, []byte(line(A, "first")+`{"x":"/a-unrelated/path"}`+"\n"), 0o600)
	claudeRel := "projects/" + pathmap.ClaudeDir(A.ws+"/repo") + "/s1.jsonl"
	claudeA := filepath.Join(claudeRoot(A), filepath.FromSlash(claudeRel))
	os.MkdirAll(filepath.Dir(claudeA), 0o700)
	os.WriteFile(claudeA, []byte(`{"cwd":"`+A.ws+`/repo","message":"see `+claudeRoot(A)+`/x"}`+"\n"), 0o600)
	A.m.push(t)
	A.m.ReleaseLeases()

	// Machine B: restored with B's paths, Claude in B's project directory.
	on(B)
	all, _ := B.m.List()
	for _, rs := range all {
		if _, err := B.m.Restore(rs, false); err != nil {
			t.Fatal(err)
		}
	}
	codexB := filepath.Join(B.m.codex, filepath.FromSlash(codexRel))
	got, _ := os.ReadFile(codexB)
	if want := line(B, "first") + `{"x":"/a-unrelated/path"}` + "\n"; string(got) != want {
		t.Fatalf("codex on B:\n%s\nwant\n%s", got, want)
	}
	claudeB := filepath.Join(claudeRoot(B), "projects", pathmap.ClaudeDir(B.ws+"/repo"), "s1.jsonl")
	if b, err := os.ReadFile(claudeB); err != nil || !strings.Contains(string(b), `"cwd":"`+B.ws+`/repo"`) {
		t.Fatalf("claude on B: %q %v", b, err)
	}
	// B continues: complete lines go up in A's form, a partial line waits.
	B.m.ChunkSize = 40 // a batch spans several chunks
	f, _ := os.OpenFile(codexB, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(line(B, "second") + `{"partial":"` + B.ws)
	f.Close()
	if st, err := B.m.PushOnce(); err != nil || st.Bytes == 0 {
		t.Fatalf("push from B: %+v %v", st, err)
	}
	// Restored-but-unchanged files must not be re-uploaded or reset.
	if st, _ := B.m.PushOnce(); st.Bytes != 0 {
		t.Fatalf("second push uploaded %d bytes", st.Bytes)
	}
	f, _ = os.OpenFile(codexB, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`/done"}` + "\n")
	f.Close()
	B.m.PushOnce()
	B.m.ReleaseLeases()
	all, _ = B.m.List()
	if len(all) != 2 {
		t.Fatalf("B created extra sessions: %d", len(all))
	}

	// Server copy is in A's form: A gets exactly what A would have written.
	on(A)
	for _, rs := range all {
		if rs.Meta.Agent == "codex" {
			if _, err := A.m.Restore(rs, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, _ = os.ReadFile(codexA)
	wantA := line(A, "first") + `{"x":"/a-unrelated/path"}` + "\n" + line(A, "second") + `{"partial":"` + A.ws + `/done"}` + "\n"
	if string(got) != wantA {
		t.Fatalf("origin copy:\n%s\nwant\n%s", got, wantA)
	}

	// Machine C reads both machines' lines with C's paths.
	on(C)
	all, _ = C.m.List()
	for _, rs := range all {
		if _, err := C.m.Restore(rs, false); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = os.ReadFile(filepath.Join(C.m.codex, filepath.FromSlash(codexRel)))
	if want := line(C, "first") + `{"x":"/a-unrelated/path"}` + "\n" + line(C, "second") + `{"partial":"` + C.ws + `/done"}` + "\n"; string(got) != want {
		t.Fatalf("codex on C:\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(string(got), A.home) || strings.Contains(string(got), B.home) {
		t.Fatal("foreign home left on C")
	}

	// Opting out restores byte for byte.
	D := mk("d")
	on(D)
	D.m.NoPathMap = true
	for _, rs := range all {
		if rs.Meta.Agent == "codex" {
			D.m.Restore(rs, false)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(D.m.codex, filepath.FromSlash(codexRel))); string(got) != wantA {
		t.Fatalf("NoPathMap changed content:\n%s", got)
	}
}
