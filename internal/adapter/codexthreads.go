package adapter

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Codex keeps the thread list shown by Codex Desktop (names, sidebar
// sections, recency) in $CODEX_HOME/state_<n>.sqlite. On a new machine it
// rebuilds the list from the rollout files, but names and sections are lost
// and recency comes from file mtimes. hearth therefore records each
// thread's name and section in the session metadata and re-applies them
// through the Codex app-server API after a restore (see client/codexstate.go).

// ThreadInfo is the Desktop-visible state of one Codex thread.
type ThreadInfo struct {
	Name      string `json:"name,omitempty"`
	SectionID string `json:"section_id,omitempty"` // stable for built-in sections such as Pinned
	Section   string `json:"section,omitempty"`    // name, to recreate custom sections
}

// PinnedSectionID is Codex's built-in "Pinned" sidebar section.
const PinnedSectionID = "01984de2-8f74-7c91-a3b2-5c5e937cf318"

var rolloutIDRE = regexp.MustCompile(`rollout-\d{4}-\d\d-\d\dT\d\d-\d\d-\d\d-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

// CodexThreadID extracts the thread id from a rollout path ("" if none).
// Continuation files ("<id>_<segment>.jsonl") map to the same thread.
func CodexThreadID(rel string) string {
	m := rolloutIDRE.FindStringSubmatch(filepath.Base(rel))
	if m == nil {
		return ""
	}
	return m[1]
}

// stateDB returns the newest state_<n>.sqlite in root.
func stateDB(root string) string {
	matches, _ := filepath.Glob(filepath.Join(root, "state_*.sqlite"))
	best, bestN := "", -1
	for _, m := range matches {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), "state_"), ".sqlite"))
		if err == nil && n > bestN {
			best, bestN = m, n
		}
	}
	return best
}

// ReadCodexThreads reads thread names and sections. The state database is
// opened read-only (no writes, no checkpoints); if it is missing or its
// schema changed, names fall back to session_index.jsonl.
func ReadCodexThreads(root string) map[string]ThreadInfo {
	out := map[string]ThreadInfo{}
	// session_index.jsonl: one line per rename, the last one wins.
	if f, err := os.Open(filepath.Join(root, "session_index.jsonl")); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e struct {
				ID   string `json:"id"`
				Name string `json:"thread_name"`
			}
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" && e.Name != "" {
				out[e.ID] = ThreadInfo{Name: e.Name}
			}
		}
		f.Close()
	}
	p := stateDB(root)
	if p == "" {
		return out
	}
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)")
	if err != nil {
		return out
	}
	defer db.Close()
	sections := map[string]string{}
	if rows, err := db.Query(`SELECT id, name FROM thread_sections`); err == nil {
		for rows.Next() {
			var id, name string
			if rows.Scan(&id, &name) == nil {
				sections[id] = name
			}
		}
		rows.Close()
	}
	rows, err := db.Query(`SELECT id, COALESCE(name, ''), COALESCE(thread_section_id, '') FROM threads`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, sec string
		if rows.Scan(&id, &name, &sec) != nil {
			continue
		}
		info := out[id]
		if name != "" {
			info.Name = name
		}
		info.SectionID, info.Section = sec, sections[sec]
		if info != (ThreadInfo{}) {
			out[id] = info
		} else {
			delete(out, id)
		}
	}
	return out
}

// threadCache re-reads the thread state only when its files change.
type threadCache struct {
	mu    sync.Mutex
	stamp string
	info  map[string]ThreadInfo
}

func (c *threadCache) get(root string) map[string]ThreadInfo {
	var b strings.Builder
	files := []string{filepath.Join(root, "session_index.jsonl")}
	if p := stateDB(root); p != "" {
		files = append(files, p, p+"-wal")
	}
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			b.WriteString(f + strconv.FormatInt(fi.Size(), 10) + strconv.FormatInt(fi.ModTime().UnixNano(), 10) + ";")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if b.String() != c.stamp || c.info == nil {
		c.stamp, c.info = b.String(), ReadCodexThreads(root)
	}
	return c.info
}

// LastTimestamp returns the newest "timestamp" of a JSONL transcript (Codex
// and Claude Code write one per line), used to restore file mtimes: Codex
// sorts its thread list by them.
func LastTimestamp(data []byte) (time.Time, bool) {
	tail := data
	if len(tail) > 256<<10 {
		tail = tail[len(tail)-256<<10:]
	}
	lines := bytes.Split(bytes.TrimRight(tail, "\n"), []byte("\n"))
	var best time.Time
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-50; i-- {
		var e struct {
			TS string `json:"timestamp"`
		}
		if json.Unmarshal(lines[i], &e) != nil || e.TS == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, e.TS); err == nil && t.After(best) {
			best = t
		}
	}
	return best, !best.IsZero()
}

// SortedThreadIDs is a helper for deterministic output.
func SortedThreadIDs(m map[string]ThreadInfo) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// CodexKnownThreads returns the ids of every thread in Codex's state
// database (active and archived). ok is false when the database is missing
// or unreadable: callers must not conclude a thread was deleted then.
func CodexKnownThreads(root string) (ids map[string]bool, ok bool) {
	p := stateDB(root)
	if p == "" {
		return nil, false
	}
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, false
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id FROM threads`)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	ids = map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids[id] = true
		}
	}
	return ids, rows.Err() == nil && len(ids) > 0
}
