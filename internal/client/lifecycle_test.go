package client

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

func TestDeleteTombstoneRevive(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	a.appendLines(t, 10, "x")
	a.push(t)
	sid := a.only(t).ID
	if err := a.API.DeleteSession(sid); err != nil {
		t.Fatal(err)
	}
	a.appendLines(t, 10, "y")
	if st, _ := a.PushOnce(); st.Bytes != 0 {
		t.Fatal("re-uploaded a deleted session")
	}
	if all, _ := a.List(); len(all) != 0 {
		t.Fatal("deleted session still listed")
	}
	if !a.st.get(sid).Gone {
		t.Fatal("local state not marked gone")
	}
	// A brand-new machine with the same file must also be refused (tombstone).
	b := en.machine("b")
	os.MkdirAll(filepath.Dir(b.path()), 0o700)
	os.WriteFile(b.path(), a.read(t), 0o600)
	if st, _ := b.PushOnce(); st.Bytes != 0 {
		t.Fatal("tombstone ignored by another device")
	}
	if err := a.API.Revive(sid); err != nil {
		t.Fatal(err)
	}
	a.Forget(sid)
	a.push(t)
	if a.only(t).Size != int64(len(a.read(t))) {
		t.Fatal("revived session not fully uploaded")
	}
}

func TestExpiryLifecycle(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	a.appendLines(t, 10, "x")
	a.push(t)
	sid := a.only(t).ID
	a.ReleaseLeases()
	day := 24 * time.Hour

	en.clock.add(29 * day)
	en.srv.RunGC()
	if s := a.only(t); s.Status != "active" {
		t.Fatalf("expired too early: %s", s.Status)
	}
	en.clock.add(2 * day)
	en.srv.RunGC()
	if s := a.only(t); s.Status != "expired" {
		t.Fatalf("status %s, want expired", s.Status)
	}
	// Any access during the grace period revives it.
	if _, err := a.API.Touch(sid); err != nil {
		t.Fatal(err)
	}
	if s := a.only(t); s.Status != "active" {
		t.Fatal("touch did not revive")
	}

	// Pinned sessions never expire.
	a.API.Pin(sid, true)
	en.clock.add(100 * day)
	en.srv.RunGC()
	if s := a.only(t); s.Status != "active" {
		t.Fatal("pinned session expired")
	}
	a.API.Pin(sid, false)

	en.clock.add(31 * day)
	en.srv.RunGC()
	en.clock.add(8 * day)
	st, err := en.srv.RunGC()
	if err != nil || st.Deleted != 1 {
		t.Fatalf("gc deleted %d (%v), want 1", st.Deleted, err)
	}
	if all, _ := a.List(); len(all) != 0 {
		t.Fatal("session still listed after deletion")
	}
	if !servertest.LocalBlobs() {
		return
	}
	// Objects are gone from disk.
	var files int
	filepath.WalkDir(filepath.Join(en.data, "objects"), func(_ string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			files++
		}
		return nil
	})
	if files != 0 {
		t.Fatalf("%d object files left after GC", files)
	}
	// The daemon must not resurrect it.
	a.appendLines(t, 1, "late")
	if st, _ := a.PushOnce(); st.Bytes != 0 {
		t.Fatal("expired+deleted session re-uploaded")
	}
}

func TestActiveLeasePreventsExpiry(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	a.appendLines(t, 1, "x")
	a.push(t)
	sid := a.only(t).ID
	en.clock.add(31 * 24 * time.Hour)
	if _, err := a.API.Lease(sid, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	en.srv.RunGC()
	if a.only(t).Status != "active" {
		t.Fatal("leased session expired")
	}
}

func TestTamperedChunkDetected(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	a.appendLines(t, 5, "x")
	a.push(t)
	if !servertest.LocalBlobs() {
		t.Skip("tampers with files under the local blob directory")
	}
	filepath.WalkDir(filepath.Join(en.data, "objects"), func(p string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			b[len(b)-1] ^= 1
			os.WriteFile(p, b, 0o600)
		}
		return nil
	})
	b := en.machine("b")
	if _, err := b.Restore(b.only(t), false); err == nil || !strings.Contains(err.Error(), "decryption failed") {
		t.Fatalf("tampering not detected: %v", err)
	}
}

func TestWrongPasswordAndServerSeesNoPlaintext(t *testing.T) {
	en := newEnv(t)
	if _, err := Login(t.TempDir(), en.url, "dev@example.com", []byte("wrong password"), "x"); err == nil {
		t.Fatal("login with wrong password succeeded")
	}
	a := en.machine("a")
	a.appendLines(t, 5, "SECRET_MARKER")
	a.push(t)
	filepath.WalkDir(en.data, func(p string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			for _, needle := range []string{"SECRET_MARKER", "rollout-x", "codex"} {
				if strings.Contains(string(b), needle) {
					t.Errorf("%s contains plaintext %q", p, needle)
				}
			}
		}
		return nil
	})
}

func TestMemoryNotesArePinnedAndRewritable(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	claudeA, claudeB := t.TempDir(), t.TempDir()
	a.Adapters = []adapter.Adapter{adapter.ClaudeAt(claudeA)}
	b.Adapters = []adapter.Adapter{adapter.ClaudeAt(claudeB)}
	note := filepath.Join(claudeA, "projects/-ws/memory/MEMORY.md")
	os.MkdirAll(filepath.Dir(note), 0o700)
	os.WriteFile(note, []byte("- prefers Go\n"), 0o600)
	a.push(t)
	// Memory files are rewritten in place, not appended.
	os.WriteFile(note, []byte("- prefers Go\n- uses sglang\n"), 0o600)
	a.push(t)
	rs := a.only(t)
	if !rs.Pinned || rs.Meta.Kind != adapter.KindMemory {
		t.Fatalf("memory note not pinned: %+v", rs)
	}
	en.clock.add(400 * 24 * time.Hour)
	en.srv.RunGC()
	if _, err := b.Restore(b.only(t), false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(claudeB, "projects/-ws/memory/MEMORY.md"))
	if string(got) != "- prefers Go\n- uses sglang\n" {
		t.Fatalf("restored memory: %q", got)
	}
}
