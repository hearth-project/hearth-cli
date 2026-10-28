package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A session uploaded before the origin machine had a workspace (its roots
// record only the home) still lands in the other machine's workspace,
// because the user's workspaces are known across machines; a copy restored
// earlier with the old paths is rewritten in place.
func TestKnownWorkspacesRemap(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	home := t.TempDir()
	t.Setenv("HOME", home)
	wsA, wsB := filepath.Join(home, "workspace/me"), filepath.Join(home, "jobs/me")
	rel := "sessions/2026/10/22/rollout-2026-10-22T18-15-33-01a1631b-ded4-7753-bc8b-5581cfac2963.jsonl"
	line := func(ws, s string) string {
		return `{"type":"session_meta","payload":{"cwd":"` + ws + `","note":"` + s + `","file":"` + ws + `/x.go"}}` + "\n"
	}
	// A uploads with no workspace yet (as before `hearth init`).
	t.Setenv("HEARTH_WORKSPACE", "")
	pa := filepath.Join(a.codex, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(pa), 0o700)
	os.WriteFile(pa, []byte(line(wsA, "one")), 0o600)
	a.push(t)
	a.ReleaseLeases()

	// B restores before anyone knows wsA: paths stay A's.
	t.Setenv("HEARTH_WORKSPACE", wsB)
	all, _ := b.List()
	if _, err := b.Restore(all[0], false); err != nil {
		t.Fatal(err)
	}
	pb := filepath.Join(b.codex, filepath.FromSlash(rel))
	if got, _ := os.ReadFile(pb); !strings.Contains(string(got), wsA) {
		t.Fatalf("expected the old paths first: %s", got)
	}
	// A gets its workspace (hearth init): its next pass records it in the
	// session's metadata, without re-uploading the content.
	t.Setenv("HEARTH_WORKSPACE", wsA)
	if st := a.push(t); st.Bytes != 0 {
		t.Fatalf("content re-uploaded: %+v", st)
	}
	a.ReleaseLeases()
	t.Setenv("HEARTH_WORKSPACE", wsB)
	all, _ = b.List()
	if all[0].Meta.Roots.Workspace != wsA || all[0].Meta.Cwd != wsA {
		t.Fatalf("roots not recorded: %+v", all[0].Meta)
	}
	r, err := b.Restore(all[0], false)
	if err != nil || r.Action != "remapped" {
		t.Fatalf("restore again: %+v %v", r, err)
	}
	if got, _ := os.ReadFile(pb); string(got) != line(wsB, "one") {
		t.Fatalf("remapped copy:\n%s", got)
	}
	// B continues; B's line goes up with B's paths (A did not record a
	// workspace) and A maps it back through the known list.
	f, _ := os.OpenFile(pb, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(line(wsB, "two"))
	f.Close()
	if st, err := b.PushOnce(); err != nil || st.Bytes == 0 {
		t.Fatalf("push from B: %+v %v", st, err)
	}
	b.ReleaseLeases()
	t.Setenv("HEARTH_WORKSPACE", wsA)
	all, _ = a.List()
	if _, err := a.Restore(all[0], true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(pa); string(got) != line(wsA, "one")+line(wsA, "two") {
		t.Fatalf("A sees:\n%s", got)
	}
}
