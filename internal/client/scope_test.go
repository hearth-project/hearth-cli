package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/pathmap"
)

// Several people share root on a GPU machine: only sessions run inside
// this user's workspace (or claimed directories) are uploaded.
func TestSessionScopeOnSharedAccount(t *testing.T) {
	en := newEnv(t)
	a := en.machine("shared")
	home, ws := learnEnv(t)
	claude := a.Adapters[1].Root()
	w := func(p, data string) {
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(data), 0o600)
	}
	codexLine := func(cwd string) string {
		return `{"type":"session_meta","payload":{"id":"x","cwd":"` + cwd + `"}}` + "\n"
	}
	day := filepath.Join(a.codex, "sessions/2026/10/23")
	w(filepath.Join(day, "rollout-2026-10-23T10-00-00-01a16500-0000-7000-8000-000000000001.jsonl"), codexLine(ws+"/repo"))
	w(filepath.Join(day, "rollout-2026-10-23T10-00-00-01a16500-0000-7000-8000-000000000002.jsonl"), codexLine("/root/jobs/someone-else"))
	w(filepath.Join(day, "rollout-2026-10-23T10-00-00-01a16500-0000-7000-8000-000000000003.jsonl"), codexLine(home+"/scratch"))
	mine := filepath.Join(claude, "projects", pathmap.ClaudeDir(ws+"/repo"))
	w(filepath.Join(mine, "s1.jsonl"), `{"cwd":"`+ws+`/repo","type":"user"}`+"\n")
	w(filepath.Join(mine, "s1/subagents/agent-1.jsonl"), `{"type":"user"}`+"\n") // no cwd: judged by directory
	w(filepath.Join(mine, "memory/MEMORY.md"), "my notes\n")
	theirs := filepath.Join(claude, "projects", "-root-jobs-shared-bobxing")
	w(filepath.Join(theirs, "s2.jsonl"), `{"cwd":"/root/jobs/shared/bobxing","type":"user"}`+"\n")
	w(filepath.Join(theirs, "memory/MEMORY.md"), "their notes\n")

	st := a.push(t)
	if st.Files != 4 || st.Foreign != 4 {
		t.Fatalf("uploaded %d, foreign %d", st.Files, st.Foreign)
	}
	all, _ := a.List()
	for _, rs := range all {
		if rs.Meta.Cwd == "/root/jobs/someone-else" || filepath.Dir(rs.Meta.RelPath) == "projects/-root-jobs-shared-bobxing" {
			t.Fatalf("foreign session uploaded: %s", rs.Meta.RelPath)
		}
	}
	if fs := a.ForeignSessions(); len(fs) != 4 {
		t.Fatalf("foreign list %+v", fs)
	}
	// Claiming a directory brings its sessions in; "all" takes everything.
	SaveScope(a.Home, Scope{Paths: []string{"~/scratch"}})
	if st := a.push(t); st.Foreign != 3 {
		t.Fatalf("after scope add: foreign %d", st.Foreign)
	}
	SaveScope(a.Home, Scope{All: true})
	if st := a.push(t); st.Foreign != 0 {
		t.Fatalf("scope all: foreign %d", st.Foreign)
	}
	// prune finds what was uploaded outside the workspace.
	got, err := a.ForeignOnServer(nil)
	if err != nil || len(got) != 4 {
		t.Fatalf("prune candidates %d %v", len(got), err)
	}
	// Without a workspace the home is the user's own: everything uploads.
	SaveScope(a.Home, Scope{})
	t.Setenv("HEARTH_WORKSPACE", "")
	if roots := ownedRoots(a.Home); roots != nil {
		t.Fatalf("roots without workspace: %v", roots)
	}
}

// A home without a workspace (~/.hearth) takes every session on the
// account, except those run inside a workspace made with hearth init: that
// workspace has its own login, often another person's on a shared account.
func TestNoWorkspaceSkipsOtherWorkspaces(t *testing.T) {
	en := newEnv(t)
	a := en.machine("shared")
	learnEnv(t)
	t.Setenv("HEARTH_WORKSPACE", "")
	bob := t.TempDir()
	if _, err := InitWorkspace(bob); err != nil {
		t.Fatal(err)
	}
	day := filepath.Join(a.codex, "sessions/2026/10/23")
	os.MkdirAll(day, 0o700)
	line := func(cwd string) []byte {
		return []byte(`{"type":"session_meta","payload":{"id":"x","cwd":"` + cwd + `"}}` + "\n")
	}
	os.WriteFile(filepath.Join(day, "rollout-2026-10-23T10-00-00-01a16500-0000-7000-8000-000000000011.jsonl"), line("/srv/mine"), 0o600)
	os.WriteFile(filepath.Join(day, "rollout-2026-10-23T10-00-00-01a16500-0000-7000-8000-000000000012.jsonl"), line(bob+"/repo"), 0o600)

	if st := a.push(t); st.Files != 1 || st.Foreign != 1 {
		t.Fatalf("uploaded %d, foreign %d", st.Files, st.Foreign)
	}
	if fs := a.ForeignSessions(); len(fs) != 1 || fs[0].Cwd != bob+"/repo" {
		t.Fatalf("foreign list %+v", fs)
	}
	// "every session on this account is mine" still takes it.
	SaveScope(a.Home, Scope{All: true})
	if st := a.push(t); st.Foreign != 0 {
		t.Fatalf("scope all: foreign %d", st.Foreign)
	}
}
