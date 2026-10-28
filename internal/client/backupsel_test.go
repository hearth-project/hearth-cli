package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/backup"
)

// The selection decides what is uploaded: disabled categories and "!"
// exclusions are skipped, listed home files are uploaded and restored
// under the other machine's home.
func TestBackupSelection(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	homeA, homeB := t.TempDir(), t.TempDir()
	t.Setenv("HOME", homeA)
	t.Setenv("HEARTH_WORKSPACE", "")

	// A Codex session and a chosen ~/.gitconfig; ~/.ssh/id_old excluded.
	a.appendLines(t, 3, "x")
	os.WriteFile(filepath.Join(homeA, ".gitconfig"), []byte("[user]\n\tname = a\n"), 0o600)
	os.MkdirAll(filepath.Join(homeA, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(homeA, ".ssh", "config"), []byte("Host x\n"), 0o600)
	os.WriteFile(filepath.Join(homeA, ".ssh", "id_old"), []byte("old"), 0o600)
	a.Adapters = append(a.Adapters, selectedAdapter(a.Home))
	b.Adapters = append(b.Adapters, selectedAdapter(b.Home))
	conf := BackupConfigPath(a.Home)
	os.MkdirAll(filepath.Dir(conf), 0o755)
	os.WriteFile(conf, []byte("!@sessions\n~/.gitconfig\n~/.ssh/\n!~/.ssh/id_old\n"), 0o644)

	st := a.push(t)
	if st.Unselected != 1 { // the Codex session: @sessions is off
		t.Fatalf("unselected=%d", st.Unselected)
	}
	all, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, rs := range all {
		got[rs.Meta.Agent+"/"+rs.Meta.RelPath] = true
	}
	if len(got) != 2 || !got["files/home/.gitconfig"] || !got["files/home/.ssh/config"] {
		t.Fatalf("uploaded %v", got)
	}

	// Turn sessions on: the next pass uploads the conversation.
	if err := backup.SetCategory(conf, backup.Sessions, true); err != nil { // replaces !@sessions
		t.Fatal(err)
	}
	if st := a.push(t); st.Unselected != 0 || st.Uploaded == 0 {
		t.Fatalf("after enabling sessions: %+v", st)
	}

	// B restores the chosen files under its own home.
	t.Setenv("HOME", homeB)
	for _, rs := range all {
		if _, err := b.Restore(rs, false); err != nil {
			t.Fatal(err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(homeB, ".gitconfig")); err != nil || string(data) != "[user]\n\tname = a\n" {
		t.Fatalf("restored .gitconfig: %q %v", data, err)
	}
	_ = adapter.KindSelected
}

// Credentials: offered while undecided, recorded when accepted, never
// offered after "!@credentials".
func TestCredentialChoiceIsRecorded(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	_, ws := learnEnv(t)
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n"
	os.MkdirAll(filepath.Join(ws, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(ws, ".ssh/id_ed25519"), []byte(key), 0o600)
	id := "ssh-key:${WORKSPACE}/.ssh/id_ed25519"

	f, ok := findingIDs(a.Discover())[id]
	if !ok {
		t.Fatal("credential not offered while undecided")
	}
	learned, _ := LoadLearned(a.Home)
	if _, err := a.Accept(f, learned); err != nil {
		t.Fatal(err)
	}
	if !BackupSelection(a.Home).Enabled(backup.Credentials) {
		t.Fatal("credentials are on by default")
	}
	if err := backup.SetCategory(BackupConfigPath(a.Home), backup.Credentials, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := findingIDs(a.Discover())[id]; ok {
		t.Fatal("credential offered after !@credentials")
	}
}

// Agents are selected separately: Codex uploads, Claude Code does not.
func TestBackupPerAgent(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	a.appendLines(t, 2, "codex")
	claudeRoot := a.Adapters[1].Root()
	p := filepath.Join(claudeRoot, "projects", "-ws", "s.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"type":"user","cwd":"/ws","message":{"role":"user","content":"hi"}}`+"\n"), 0o600)
	conf := BackupConfigPath(a.Home)
	os.MkdirAll(filepath.Dir(conf), 0o755)
	os.WriteFile(conf, []byte("@codex\n!@claude\n"), 0o644)
	if st := a.push(t); st.Unselected != 1 {
		t.Fatalf("unselected=%d, want the Claude session", st.Unselected)
	}
	all, _ := b.List()
	if len(all) != 1 || all[0].Meta.Agent != "codex" {
		t.Fatalf("uploaded %d sessions", len(all))
	}
}
