package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/client"
)

func TestRecentUpdatesLine(t *testing.T) {
	home := t.TempDir()
	if recentUpdatesLine(home) != "" {
		t.Fatal("a line without updates")
	}
	now := time.Now()
	ups := []client.LinkUpdate{ // newest first, as they are stored
		{Time: now.Add(-1 * time.Hour).Unix(), Type: "skill", Name: "bench", Source: "team", From: 2, To: 3},
		{Time: now.Add(-5 * time.Hour).Unix(), Type: "skill", Name: "bench", Source: "team", From: 1, To: 2},
		{Time: now.Add(-6 * time.Hour).Unix(), Type: "agent", Name: "rev", Source: "team", From: 4, To: 5},
		{Time: now.Add(-10 * 24 * time.Hour).Unix(), Type: "skill", Name: "old", Source: "team", From: 1, To: 2}, // outside the week
	}
	b, _ := json.Marshal(ups)
	if err := os.WriteFile(filepath.Join(home, "link-updates.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	line := recentUpdatesLine(home)
	// Two steps of one skill make one entry from the first to the last version.
	if !strings.Contains(line, "bench v1 → v3") || !strings.Contains(line, "rev v4 → v5") || strings.Contains(line, "old") {
		t.Fatalf("line = %q", line)
	}
}

func TestLinkMode(t *testing.T) {
	team := client.Source{Name: "team", Type: "team", Trust: "trusted"}
	git := client.Source{Name: "g", Type: "git", Trust: "trusted"}
	t.Setenv("HEARTH_HOME", t.TempDir())
	for _, c := range []struct {
		l    client.Link
		s    client.Source
		want string
	}{
		{client.Link{Version: 2}, team, "follows the newest version"},
		{client.Link{Version: 2, Pinned: true}, team, "pinned"},
		{client.Link{Version: 2, Auto: true}, team, "automatic"},
		{client.Link{Commit: "abc"}, git, "pinned to the commit"},
		{client.Link{Version: 1}, client.Source{Name: "x", Type: "team", Trust: "untrusted"}, "stays at this version"},
	} {
		if got := linkMode(c.l, c.s); !strings.Contains(got, c.want) {
			t.Errorf("%+v: %q, want %q", c.l, got, c.want)
		}
	}
}
