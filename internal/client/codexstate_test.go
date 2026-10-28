package client

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
)

// TestMain doubles as a fake `codex app-server` (CodexBinary = this test
// binary), so the apply logic is tested without Codex installed.
func TestMain(m *testing.M) {
	if st := os.Getenv("HEARTH_FAKE_CODEX_STATE"); st != "" {
		fakeAppServer(st)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeState struct {
	Threads  map[string]appThread `json:"threads"`
	Sections map[string]string    `json:"sections"` // id -> name
	Calls    []string             `json:"calls"`
}

func fakeAppServer(path string) {
	var st fakeState
	b, _ := os.ReadFile(path)
	json.Unmarshal(b, &st)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	out := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var p map[string]any
		json.Unmarshal(req.Params, &p)
		st.Calls = append(st.Calls, req.Method)
		var res any = map[string]any{}
		switch req.Method {
		case "thread/list":
			var data []appThread
			if p["archived"] != true {
				for _, t := range st.Threads {
					data = append(data, t)
				}
			}
			res = map[string]any{"data": data, "nextCursor": nil}
		case "threadSection/list":
			var data []map[string]string
			for id, n := range st.Sections {
				data = append(data, map[string]string{"id": id, "name": n})
			}
			res = map[string]any{"data": data}
		case "threadSection/create":
			id := "new-" + p["name"].(string)
			st.Sections[id] = p["name"].(string)
			res = map[string]any{"section": map[string]string{"id": id, "name": p["name"].(string)}}
		case "thread/name/set":
			t := st.Threads[p["threadId"].(string)]
			t.Name = p["name"].(string)
			st.Threads[t.ID] = t
		case "thread/section/move":
			t := st.Threads[p["threadId"].(string)]
			sid := p["sectionId"].(string)
			t.Section = &struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{sid, st.Sections[sid]}
			st.Threads[t.ID] = t
		}
		out.Encode(map[string]any{"jsonrpc": "2.0", "method": "noise/notification"}) // must be skipped
		out.Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": res})
	}
	b, _ = json.Marshal(st)
	os.WriteFile(path, b, 0o600)
}

const (
	thread1 = "01a162cf-d71d-7952-b0f0-10083a5ddb85"
	thread2 = "01a1634a-3d03-7ca2-b87a-324fc86611cd"
)

func TestCodexThreadsSyncAcrossMachines(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	// Machine A: two Codex threads, one renamed and pinned, one in a custom section.
	rel1 := "sessions/2026/10/22/rollout-2026-10-22T16-52-30-" + thread1 + ".jsonl"
	rel2 := "sessions/2026/10/22/rollout-2026-10-22T20-46-03-" + thread2 + "_01a163a5-aaa7-7271-a561-dc6788338284.jsonl"
	line := func(ts string) string { return `{"timestamp":"` + ts + `","type":"event_msg"}` + "\n" }
	for rel, ts := range map[string]string{rel1: "2026-10-22T11:03:20.249Z", rel2: "2026-10-22T15:00:00Z"} {
		p := filepath.Join(a.codex, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(line("2026-10-22T08:00:00Z")+line(ts)), 0o600)
	}
	index := filepath.Join(a.codex, "session_index.jsonl")
	os.WriteFile(index, []byte(`{"id":"`+thread1+`","thread_name":"hello thread"}`+"\n"), 0o600)
	a.push(t)
	all, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	want := CodexThreadsToApply(all)
	if want[thread1].Name != "hello thread" || len(want) != 1 {
		t.Fatalf("after first push: %+v", want)
	}
	// Rename later without touching the rollout: only the metadata is re-sent.
	f, _ := os.OpenFile(index, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"id":"` + thread2 + `","thread_name":"hearth design"}` + "\n")
	f.WriteString(`{"id":"` + thread1 + `","thread_name":"hello, renamed"}` + "\n")
	f.Close()
	if st, err := a.PushOnce(); err != nil || st.Bytes != 0 {
		t.Fatalf("rename re-uploaded content: %+v %v", st, err)
	}
	all, _ = b.List()
	want = CodexThreadsToApply(all)
	if want[thread1].Name != "hello, renamed" || want[thread2].Name != "hearth design" {
		t.Fatalf("after rename: %+v", want)
	}

	// Machine B: restore keeps last-activity mtimes (Codex sorts by them).
	for _, rs := range all {
		if _, err := b.Restore(rs, false); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(filepath.Join(b.codex, filepath.FromSlash(rel1)))
	if err != nil || !fi.ModTime().Equal(time.Date(2026, 10, 22, 11, 3, 20, 249e6, time.UTC)) {
		t.Fatalf("mtime %v %v", fi.ModTime(), err)
	}
	// B has no names yet: its next push must not clear the server's.
	b.PushOnce()
	all, _ = b.List()
	if got := CodexThreadsToApply(all); got[thread1].Name != "hello, renamed" {
		t.Fatalf("restored machine cleared names: %+v", got)
	}

	// Apply through the (fake) app-server: rename, pin, create the custom section.
	state := filepath.Join(t.TempDir(), "codex-state.json")
	init, _ := json.Marshal(fakeState{
		Threads:  map[string]appThread{thread1: {ID: thread1}, thread2: {ID: thread2, Name: "hearth design"}},
		Sections: map[string]string{adapter.PinnedSectionID: "Pinned"},
	})
	os.WriteFile(state, init, 0o600)
	t.Setenv("HEARTH_FAKE_CODEX_STATE", state)
	old := CodexBinary
	CodexBinary = os.Args[0]
	defer func() { CodexBinary = old }()
	apply := map[string]adapter.ThreadInfo{
		thread1:                                {Name: "hello, renamed", SectionID: adapter.PinnedSectionID, Section: "Pinned"},
		thread2:                                {Name: "hearth design", SectionID: "sec-on-a", Section: "Research"},
		"01a16000-0000-7000-8000-000000000000": {Name: "not restored here"},
	}
	st, err := ApplyCodexThreads(b.codex, apply)
	if err != nil {
		t.Fatal(err)
	}
	if st.Named != 1 || st.Sectioned != 2 || st.Missing != 1 {
		t.Fatalf("stats %+v", st)
	}
	var after fakeState
	raw, _ := os.ReadFile(state)
	json.Unmarshal(raw, &after)
	if t1 := after.Threads[thread1]; t1.Name != "hello, renamed" || t1.Section == nil || t1.Section.ID != adapter.PinnedSectionID {
		t.Fatalf("thread1 %+v", t1)
	}
	if t2 := after.Threads[thread2]; t2.Section == nil || t2.Section.Name != "Research" {
		t.Fatalf("thread2 %+v", t2)
	}
	// Second run: nothing to change.
	if st, err := ApplyCodexThreads(b.codex, apply); err != nil || st.Named+st.Sectioned != 0 || st.Unchanged != 2 {
		t.Fatalf("second apply %+v %v", st, err)
	}
}

// Codex archive/unarchive moves a rollout; it must stay the same session
// (no duplicate upload) and a later restore must update it in place.
func TestCodexArchiveMoveKeepsSession(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	base := "rollout-2026-10-22T16-52-30-" + thread1 + ".jsonl"
	rel := "sessions/2026/10/22/" + base
	p := filepath.Join(a.codex, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"timestamp":"2026-10-22T08:00:00Z","type":"event_msg"}`+"\n"), 0o600)
	a.push(t)
	arch := filepath.Join(a.codex, "archived_sessions", base)
	os.MkdirAll(filepath.Dir(arch), 0o700)
	if err := os.Rename(p, arch); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(arch, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"timestamp":"2026-10-22T09:00:00Z","type":"event_msg"}` + "\n")
	f.Close()
	a.push(t)
	all, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("archived rollout uploaded as a new session: %d sessions", len(all))
	}
	// B restores, archives it locally, then restores the newer copy again.
	if _, err := b.Restore(all[0], false); err != nil {
		t.Fatal(err)
	}
	bp := filepath.Join(b.codex, filepath.FromSlash(rel))
	barch := filepath.Join(b.codex, "archived_sessions", base)
	os.MkdirAll(filepath.Dir(barch), 0o700)
	os.Rename(bp, barch)
	r, err := b.Restore(all[0], false)
	if err != nil || r.Path != barch || r.Action != "up-to-date" {
		t.Fatalf("restore after archive: %+v %v", r, err)
	}
	// Its next push uses the same session, not a new one for the new name.
	b.push(t)
	if all2, _ := b.List(); len(all2) != 1 {
		for _, x := range all2 {
			t.Logf("session %s %s", x.ID[:12], x.Meta.RelPath)
		}
		t.Fatalf("archived copy on b uploaded as a new session: %d", len(all2))
	}
	if _, err := os.Stat(bp); err == nil {
		t.Fatal("restore wrote a second copy into sessions/")
	}
}

// A thread deleted in Codex (rollout and state row gone) is marked expired
// on the server and not restored elsewhere; an archived one is untouched.
func TestCodexDeletedThreadExpires(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	ids := []string{thread1, thread2, "01a16000-0000-7000-8000-00000000000a", "01a16000-0000-7000-8000-00000000000b"}
	var paths []string
	for i, id := range ids {
		p := filepath.Join(a.codex, "sessions", "2026", "10", "22", fmt.Sprintf("rollout-2026-10-22T16-52-3%d-%s.jsonl", i, id))
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(`{"timestamp":"2026-10-22T08:00:00Z","type":"event_msg"}`+"\n"), 0o600)
		paths = append(paths, p)
	}
	db := filepath.Join(a.codex, "state_5.sqlite")
	setThreads := func(ids ...string) {
		os.Remove(db)
		c, err := sql.Open("sqlite", db)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Exec(`CREATE TABLE threads(id TEXT PRIMARY KEY, name TEXT, thread_section_id TEXT)`); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			c.Exec(`INSERT INTO threads(id) VALUES(?)`, id)
		}
	}
	setThreads(ids...)
	a.push(t)

	// Delete thread1, archive thread2.
	os.Remove(paths[0])
	arch := filepath.Join(a.codex, "archived_sessions", filepath.Base(paths[1]))
	os.MkdirAll(filepath.Dir(arch), 0o700)
	os.Rename(paths[1], arch)
	setThreads(ids[1:]...)
	if st := a.push(t); st.Deleted != 1 {
		t.Fatalf("deleted=%d", st.Deleted)
	}
	all, _ := b.List()
	status := map[string]string{}
	for _, rs := range all {
		status[adapter.CodexThreadID(rs.Meta.RelPath)] = rs.Status
	}
	if status[thread1] != "expired" || status[thread2] != "active" || len(all) != 4 {
		t.Fatalf("status %v", status)
	}
	if st := a.push(t); st.Deleted != 0 {
		t.Fatal("expired twice")
	}

	// A wiped CODEX_HOME (everything gone) must not expire anything.
	for _, p := range paths[2:] {
		os.Remove(p)
	}
	os.Remove(arch)
	setThreads("01a16000-0000-7000-8000-0000000000ff")
	if st := a.push(t); st.Deleted != 0 {
		t.Fatalf("mass deletion expired %d sessions", st.Deleted)
	}
}

// Deleting the Desktop copy of a restored thread expires the original too.
func TestDeletingDesktopForkExpiresOriginal(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	orig, fork := thread1, thread2
	mk := func(dir, id string) string {
		p := filepath.Join(a.codex, dir, "rollout-2026-10-22T16-52-30-"+id+".jsonl")
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(`{"timestamp":"2026-10-22T08:00:00Z","type":"event_msg"}`+"\n"), 0o600)
		return p
	}
	mk("archived_sessions", orig)
	fp := mk("sessions/2026/10/22", fork)
	extra := mk("sessions/2026/10/22", "01a16000-0000-7000-8000-00000000000a")
	_ = extra
	db := filepath.Join(a.codex, "state_5.sqlite")
	c, _ := sql.Open("sqlite", db)
	c.Exec(`CREATE TABLE threads(id TEXT PRIMARY KEY, name TEXT, thread_section_id TEXT)`)
	for _, id := range []string{orig, fork, "01a16000-0000-7000-8000-00000000000a"} {
		c.Exec(`INSERT INTO threads(id) VALUES(?)`, id)
	}
	a.push(t)
	if err := a.RecordDesktopFork(DesktopFork{Original: orig, Fork: fork}); err != nil {
		t.Fatal(err)
	}
	os.Remove(fp)
	c.Exec(`DELETE FROM threads WHERE id=?`, fork)
	c.Close()
	if st := a.push(t); st.Deleted != 2 {
		t.Fatalf("deleted=%d, want fork and original", st.Deleted)
	}
}

// Sessions restored into another Codex home are not "deleted" here.
func TestRestoredElsewhereIsNotDeleted(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	for i, id := range []string{thread1, thread2} {
		p := filepath.Join(a.codex, "sessions", "2026", "10", "22", fmt.Sprintf("rollout-2026-10-22T16-52-3%d-%s.jsonl", i, id))
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(`{"timestamp":"2026-10-22T08:00:00Z","type":"event_msg"}`+"\n"), 0o600)
	}
	a.push(t)
	// b restores into a different Codex home than the one it syncs.
	other := t.TempDir()
	saved := b.Adapters
	b.Adapters = []adapter.Adapter{adapter.CodexAt(other)}
	all, _ := b.List()
	for _, rs := range all {
		if _, err := b.Restore(rs, false); err != nil {
			t.Fatal(err)
		}
	}
	b.Adapters = saved
	c, _ := sql.Open("sqlite", filepath.Join(b.codex, "state_5.sqlite"))
	c.Exec(`CREATE TABLE threads(id TEXT PRIMARY KEY, name TEXT, thread_section_id TEXT)`)
	c.Exec(`INSERT INTO threads(id) VALUES('01a16000-0000-7000-8000-00000000000a')`)
	c.Close()
	if st := b.push(t); st.Deleted != 0 {
		t.Fatalf("expired %d sessions restored elsewhere", st.Deleted)
	}
}
