package adapter

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	tid  = "01a162cf-d71d-7952-b0f0-10083a5ddb85"
	tid2 = "01a1634a-3d03-7ca2-b87a-324fc86611cd"
)

// fakeStateDB creates the subset of Codex's state_5.sqlite hearth reads.
func fakeStateDB(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(root, "state_5.sqlite")+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT, name TEXT, thread_section_id TEXT, archived INTEGER)`,
		`CREATE TABLE thread_sections (id TEXT PRIMARY KEY, name TEXT NOT NULL, appearance TEXT)`,
		`INSERT INTO thread_sections VALUES ('` + PinnedSectionID + `', 'Pinned', NULL), ('sec-1', 'Research', NULL)`,
		`INSERT INTO threads VALUES ('` + tid + `', '', 'from db', '` + PinnedSectionID + `', 0)`,
		`INSERT INTO threads VALUES ('` + tid2 + `', '', NULL, 'sec-1', 0)`,
		`INSERT INTO threads VALUES ('01a162c0-5447-7472-9a21-840a5b8748cc', '', NULL, NULL, 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestCodexThreadID(t *testing.T) {
	for in, want := range map[string]string{
		"sessions/2026/10/22/rollout-2026-10-22T16-52-30-" + tid + ".jsonl":                                       tid,
		"sessions/2026/10/22/rollout-2026-10-22T20-46-03-" + tid2 + "_01a163a5-aaa7-7271-a561-dc6788338284.jsonl": tid2,
		"archived_sessions/rollout-2026-10-22T16-52-30-" + tid + ".jsonl":                                         tid,
		"sessions/2026/10/22/notes.jsonl":                                                                         "",
	} {
		if got := CodexThreadID(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

func TestReadCodexThreads(t *testing.T) {
	root := t.TempDir()
	// Only session_index.jsonl (no state DB yet): names from the index, last line wins.
	os.WriteFile(filepath.Join(root, "session_index.jsonl"), []byte(
		`{"id":"`+tid2+`","thread_name":"old","updated_at":"x"}`+"\n"+
			`{"id":"`+tid2+`","thread_name":"hearth design","updated_at":"y"}`+"\n"+
			"not json\n"), 0o600)
	got := ReadCodexThreads(root)
	if got[tid2].Name != "hearth design" || len(got) != 1 {
		t.Fatalf("index only: %+v", got)
	}
	fakeStateDB(t, root)
	got = ReadCodexThreads(root)
	if got[tid] != (ThreadInfo{Name: "from db", SectionID: PinnedSectionID, Section: "Pinned"}) {
		t.Fatalf("db thread: %+v", got[tid])
	}
	// NULL name in the db keeps the index name; section comes from the db.
	if got[tid2] != (ThreadInfo{Name: "hearth design", SectionID: "sec-1", Section: "Research"}) {
		t.Fatalf("merged thread: %+v", got[tid2])
	}
	if _, ok := got["01a162c0-5447-7472-9a21-840a5b8748cc"]; ok || len(got) != 2 {
		t.Fatalf("thread without name or section reported: %+v", got)
	}
	// A schema Codex changed later degrades to the index, never errors.
	bad := t.TempDir()
	db, _ := sql.Open("sqlite", "file:"+filepath.Join(bad, "state_9.sqlite"))
	db.Exec(`CREATE TABLE threads (id TEXT)`)
	db.Close()
	if got := ReadCodexThreads(bad); len(got) != 0 {
		t.Fatalf("bad schema: %+v", got)
	}
}

func TestCodexAdapterAttachesThreads(t *testing.T) {
	root := t.TempDir()
	db := fakeStateDB(t, root)
	p := filepath.Join(root, "sessions/2026/10/22/rollout-2026-10-22T16-52-30-"+tid+".jsonl")
	touch(t, p)
	touch(t, filepath.Join(root, "sessions/2026/10/22/rollout-2026-10-22T16-35-34-01a162c0-5447-7472-9a21-840a5b8748cc.jsonl"))
	a := CodexAt(root)
	fs, err := a.Sessions()
	if err != nil || len(fs) != 2 {
		t.Fatal(fs, err)
	}
	for _, f := range fs {
		if CodexThreadID(f.RelPath) == tid {
			if f.Thread == nil || f.Thread.Name != "from db" {
				t.Fatalf("thread info: %+v", f.Thread)
			}
		} else if f.Thread != nil {
			t.Fatalf("unnamed thread got info %+v", f.Thread)
		}
	}
	// A rename in Codex is picked up on the next scan (WAL file changes).
	if _, err := db.Exec(`UPDATE threads SET name='renamed' WHERE id=?`, tid); err != nil {
		t.Fatal(err)
	}
	fs, _ = a.Sessions()
	for _, f := range fs {
		if CodexThreadID(f.RelPath) == tid && f.Thread.Name != "renamed" {
			t.Fatalf("rename not seen: %+v", f.Thread)
		}
	}
}

func TestLastTimestamp(t *testing.T) {
	data := []byte(`{"timestamp":"2026-10-22T10:14:09.218Z","type":"a"}` + "\n" +
		`{"timestamp":"2026-10-22T11:03:20.249Z","type":"b"}` + "\n" + `{"type":"no ts"}` + "\n")
	got, ok := LastTimestamp(data)
	if !ok || !got.Equal(time.Date(2026, 10, 22, 11, 3, 20, 249e6, time.UTC)) {
		t.Fatal(got, ok)
	}
	if _, ok := LastTimestamp([]byte("plain text\n")); ok {
		t.Fatal("timestamp found in non-JSON")
	}
}
