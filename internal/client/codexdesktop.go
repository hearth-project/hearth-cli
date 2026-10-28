package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
)

// An open Codex Desktop window loads its sidebar once and afterwards only
// adds threads its backend announces with thread/started. A rollout restored
// behind its back is indexed (Search finds it) but not listed. Forking the
// restored thread through the Desktop backend announces a new thread that
// shares the restored history (a fork rollout references its parent instead
// of copying it), so it appears at once. The restored original is archived
// so a later restart does not list the conversation twice.

// ErrNoDesktop means no Codex Desktop backend is running on this machine.
var ErrNoDesktop = errors.New("no running Codex Desktop backend")

// DesktopFork is the visible copy of a restored thread.
type DesktopFork struct {
	Original, Fork string
}

// ShowInDesktop makes a restored thread appear in the open Codex Desktop
// sidebar. prevFork, if set, is an earlier copy made for the same thread; it
// is archived (the new fork includes everything it had). info carries the
// thread's name and section.
func ShowInDesktop(codexHome, threadID, prevFork string, info *adapter.ThreadInfo) (DesktopFork, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := dialDesktop(ctx, codexHome)
	if err != nil {
		return DesktopFork{}, ErrNoDesktop
	}
	defer s.close()
	// Unarchive first: an original archived by an earlier copy may have
	// been appended to since.
	_ = s.call("thread/unarchive", map[string]any{"threadId": threadID}, nil)
	var r struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := s.call("thread/fork", map[string]any{"threadId": threadID}, &r); err != nil {
		return DesktopFork{}, err
	}
	out := DesktopFork{Original: threadID, Fork: r.Thread.ID}
	var errs []error
	if info != nil && (info.Name != "" || info.Section != "" || info.SectionID != "") {
		if _, err := s.applyThreads(map[string]adapter.ThreadInfo{out.Fork: *info}, nil); err != nil {
			errs = append(errs, err)
		}
	}
	for _, id := range []string{threadID, prevFork} {
		if id != "" && id != out.Fork {
			if err := s.call("thread/archive", map[string]any{"threadId": id}, nil); err != nil && !strings.Contains(err.Error(), "archived") {
				errs = append(errs, err)
			}
		}
	}
	return out, errors.Join(errs...)
}

// CodexForkParent returns the thread a Codex rollout was forked from.
func CodexForkParent(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	line, _ := r.ReadBytes('\n')
	var m struct {
		Type    string `json:"type"`
		Payload struct {
			ForkedFrom string `json:"forked_from_id"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &m) != nil || m.Type != "session_meta" {
		return ""
	}
	return m.Payload.ForkedFrom
}

// CodexRolloutExists reports whether a rollout of thread id is on disk
// (active or archived).
func CodexRolloutExists(codexHome, id string) bool {
	// Continuation segments are "<id>_<segment>.jsonl".
	for _, pat := range []string{"sessions/*/*/*/rollout-*" + id + "*.jsonl", "archived_sessions/rollout-*" + id + "*.jsonl"} {
		if m, _ := filepath.Glob(filepath.Join(codexHome, pat)); len(m) > 0 {
			return true
		}
	}
	return false
}

// desktopForks remembers the visible copy made for each restored thread.
type desktopForks map[string]string

func (e *Engine) forksPath() string { return filepath.Join(e.Home, "codex-desktop-forks.json") }

// DesktopForkOf returns the copy made earlier for a restored thread.
func (e *Engine) DesktopForkOf(threadID string) string {
	var m desktopForks
	_ = loadJSON(e.forksPath(), &m)
	return m[threadID]
}

// RecordDesktopFork stores the copy made for a restored thread.
func (e *Engine) RecordDesktopFork(f DesktopFork) error {
	m := desktopForks{}
	_ = loadJSON(e.forksPath(), &m)
	if m == nil {
		m = desktopForks{}
	}
	m[f.Original] = f.Fork
	return saveJSON(e.forksPath(), m)
}
