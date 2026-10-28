package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/client"
)

func TestDisplayWidth(t *testing.T) {
	if got := dispWidth("ab千帆"); got != 6 {
		t.Fatalf("dispWidth = %d, want 6", got)
	}
	if got := truncW("千帆的压测工具可以看到吗", 9); dispWidth(got) > 9 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncW = %q (%d columns)", got, dispWidth(got))
	}
	if got := truncW("short", 9); got != "short" {
		t.Fatalf("truncW short = %q", got)
	}
}

func TestTableAlignsWideText(t *testing.T) {
	tb := &table{}
	tb.add("中文标题", "x")
	tb.add("ab", "y")
	var b bytes.Buffer
	tb.write(&b)
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if dispWidth(lines[0][:strings.LastIndex(lines[0], "x")]) != dispWidth(lines[1][:strings.LastIndex(lines[1], "y")]) {
		t.Fatalf("columns not aligned:\n%s", b.String())
	}
}

func TestShortHost(t *testing.T) {
	for in, want := range map[string]string{"gpu-01.dc.example.com": "gpu-01", "10.0.0.1": "10.0.0.1", "laptop": "laptop"} {
		if got := shortHost(in); got != want {
			t.Errorf("shortHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParsePick(t *testing.T) {
	idx, mode, err := parsePick("1 3, 5-6\n", 8)
	if err != nil || mode != "pick" || !reflect.DeepEqual(idx, []int{0, 2, 4, 5}) {
		t.Fatalf("parsePick = %v %q %v", idx, mode, err)
	}
	for in, want := range map[string]string{"": "default", "a": "all", "q": "cancel"} {
		if _, mode, _ := parsePick(in, 3); mode != want {
			t.Errorf("parsePick(%q) mode %q, want %q", in, mode, want)
		}
	}
	for _, bad := range []string{"0", "9", "x", "3-1"} {
		if _, _, err := parsePick(bad, 8); err == nil {
			t.Errorf("parsePick(%q) accepted", bad)
		}
	}
}

func TestListConversationsMergesParts(t *testing.T) {
	part := func(id, rel string, size int64, turns int) client.RemoteSession {
		var rs client.RemoteSession
		rs.ID, rs.Size, rs.Status = id, size, "active"
		rs.Meta.Agent, rs.Meta.RelPath, rs.Meta.Turns = "codex", rel, turns
		return rs
	}
	all := []client.RemoteSession{
		part("aaaaaaaaaaaa01", "sessions/2026/10/24/rollout-2026-10-24T10-00-00-0199aaaa-0000-7000-8000-000000000001.jsonl", 10, 3),
		part("bbbbbbbbbbbb02", "sessions/2026/10/24/rollout-2026-10-24T09-00-00-0199aaaa-0000-7000-8000-000000000002.jsonl", 5, 1),
	}
	rows := listConversations(all, listOpts{})
	if len(rows) != 2 {
		t.Fatalf("distinct threads merged: %d rows", len(rows))
	}
	if rows[0].ID != "aaaaaaaaaaaa01" || rows[0].Bytes != 10 {
		t.Fatalf("row 0 = %+v", rows[0])
	}
	if got := listConversations(all, listOpts{query: "T09"}); len(got) != 1 || got[0].ID != "bbbbbbbbbbbb02" {
		t.Fatalf("search = %+v", got)
	}
}

func TestFoldChildren(t *testing.T) {
	row := func(id, thread, parent, kind string, turns int) client.RemoteSession {
		var rs client.RemoteSession
		rs.ID, rs.Status, rs.Size = id+"0000000000", "active", 1
		rs.Meta.Agent, rs.Meta.Turns, rs.Meta.Parent, rs.Meta.ParentKind = "codex", turns, parent, kind
		rs.Meta.RelPath = "sessions/2026/10/24/rollout-2026-10-24T10-00-00-" + thread + ".jsonl"
		rs.Meta.Title = "t " + id
		return rs
	}
	p := "0199aaaa-0000-7000-8000-000000000001"
	all := []client.RemoteSession{
		row("copy", "0199aaaa-0000-7000-8000-000000000002", p, "fork", 0),
		row("parent", p, "", "", 5),
		row("sub", "0199aaaa-0000-7000-8000-000000000003", p, "subagent", 2),
		row("realfork", "0199aaaa-0000-7000-8000-000000000004", p, "fork", 3),
		row("orphan", "0199aaaa-0000-7000-8000-000000000005", "0199aaaa-0000-7000-8000-00000000ffff", "fork", 0),
	}
	rows := listConversations(all, listOpts{})
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want parent, realfork, orphan", len(rows))
	}
	if rows[0].ID[:6] != "parent" || len(rows[0].Folded) != 2 || len(rows[0].Subagents) != 1 {
		t.Fatalf("parent row %+v", rows[0])
	}
}
