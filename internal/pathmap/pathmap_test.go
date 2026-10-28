package pathmap

import "testing"

func TestBuildAndApply(t *testing.T) {
	m := Build(Roots{Home: "/root", Workspace: "/root/workspace/me"}, Roots{Home: "/Users/me", Workspace: "/Users/me/work"}, true)
	cases := map[string]string{
		`{"cwd":"/root/workspace/me","x":"/root/workspace/me/repo/a.go"}`: `{"cwd":"/Users/me/work","x":"/Users/me/work/repo/a.go"}`,
		`cat /root/.bashrc && ls /root`:                                   `cat /Users/me/.bashrc && ls /Users/me`,
		// Other paths under home follow home; a root inside another path does not match.
		`/root/workspace/me2/x /data/root/workspace/me`:      `/Users/me/workspace/me2/x /data/root/workspace/me`,
		`~/.claude/projects/-root-workspace-me-repo/s.jsonl`: `~/.claude/projects/-Users-me-work-repo/s.jsonl`,
		`-root-workspace-me2`:                                `-Users-me-workspace-me2`,
		`/rootless /root_x "/root"`:                          `/rootless /root_x "/Users/me"`,
		`no paths here`:                                      `no paths here`,
	}
	for in, want := range cases {
		if got := m.ApplyString(in); got != want {
			t.Errorf("%s\n got %s\nwant %s", in, got, want)
		}
		if got := m.Inverse().ApplyString(want); got != in && want != in {
			t.Errorf("inverse of %s: %s", want, got)
		}
	}
	if !Build(Roots{Home: "/root"}, Roots{Home: "/root"}, true).Empty() {
		t.Fatal("identity map not empty")
	}
	if !Build(Roots{Workspace: "relative"}, Roots{Workspace: "/x"}, false).Empty() {
		t.Fatal("relative root mapped")
	}
}

func TestClaudeRel(t *testing.T) {
	m := Build(Roots{Workspace: "/root/workspace/me"}, Roots{Workspace: "/data/me"}, true)
	for in, want := range map[string]string{
		"projects/-root-workspace-me/abc.jsonl":                    "projects/-data-me/abc.jsonl",
		"projects/-root-workspace-me-sglang/abc/subagents/a.jsonl": "projects/-data-me-sglang/abc/subagents/a.jsonl",
		"projects/-root-workspace-me2/abc.jsonl":                   "projects/-root-workspace-me2/abc.jsonl",
		"projects/-tmp/abc.jsonl":                                  "projects/-tmp/abc.jsonl",
	} {
		if got := m.ClaudeRel(in); got != want {
			t.Errorf("%s: %s want %s", in, got, want)
		}
	}
	if got := m.Inverse().ClaudeRel("projects/-data-me-sglang/x.jsonl"); got != "projects/-root-workspace-me-sglang/x.jsonl" {
		t.Error(got)
	}
}
