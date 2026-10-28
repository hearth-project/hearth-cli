package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hearth-project/hearth-cli/internal/search"
)

func addSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	d := filepath.Join(dir, name)
	os.MkdirAll(filepath.Join(d, "scripts"), 0o755)
	os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\ndescription: "+desc+"\n---\nSteps for "+name+"\n"), 0o644)
	os.WriteFile(filepath.Join(d, "scripts", "run.sh"), []byte("echo\n"), 0o755)
}

func TestMCPToolsEndToEnd(t *testing.T) {
	project, lib := t.TempDir(), t.TempDir()
	addSkill(t, lib, "benchmark", "library benchmark skill: load test throughput")
	addSkill(t, project, "benchmark", "project benchmark skill: load test throughput")
	addSkill(t, lib, "icafe", "manage icafe cards")
	usage := filepath.Join(t.TempDir(), "usage.jsonl")
	svc := &Service{Sources: []Source{{"project", project}, {"library", lib}}, UsageLog: usage}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := svc.New("test").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	call := func(name string, args map[string]any) string {
		t.Helper()
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("%s: %v %+v", name, err, r)
		}
		return r.Content[0].(*mcp.TextContent).Text
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 3 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	out := call("search_skills", map[string]any{"query": "压测 load test throughput"})
	if !strings.HasPrefix(out, "1. benchmark") || !strings.Contains(out, "project benchmark skill") {
		t.Fatalf("search (project should shadow library):\n%s", out)
	}
	out = call("get_skill", map[string]any{"name": "icafe"})
	if !strings.Contains(out, "Steps for icafe") || !strings.Contains(out, "scripts/run.sh") {
		t.Fatalf("get_skill:\n%s", out)
	}
	if out = call("get_skill", map[string]any{"name": "nope"}); !strings.Contains(out, "No skill named") {
		t.Fatalf("missing skill: %s", out)
	}
	if out = call("list_skills", map[string]any{}); strings.Count(out, "\n- ")+1 != 2 {
		t.Fatalf("list_skills:\n%s", out)
	}
	// Index picks up new skills without restarting.
	addSkill(t, lib, "popo", "deploy static pages")
	if out = call("search_skills", map[string]any{"query": "deploy static pages"}); !strings.Contains(out, "popo") {
		t.Fatalf("new skill not indexed:\n%s", out)
	}
	if b, _ := os.ReadFile(usage); !strings.Contains(string(b), `"skill":"icafe"`) {
		t.Fatalf("usage not recorded: %q", b)
	}
}

func TestMCPSetupTools(t *testing.T) {
	var got []string
	svc := &Service{Sources: []Source{{"library", t.TempDir()}},
		Recipes: func() string { return "## learned\n[[step]]\ntype = \"manual\"\n" },
		Propose: func(r, steps, reason string) (string, error) {
			got = append(got, r, steps, reason)
			return "42-abc", nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := svc.New("test").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 5 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_setup_recipes", Arguments: map[string]any{}})
	if err != nil || !strings.Contains(r.Content[0].(*mcp.TextContent).Text, "## learned") {
		t.Fatalf("list_setup_recipes: %v %+v", err, r)
	}
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "propose_setup_step", Arguments: map[string]any{
		"steps": "[[step]]\ntype = \"run\"\nrun = \"npm i -g x\"\n", "reason": "installed x"}})
	if err != nil || !strings.Contains(r.Content[0].(*mcp.TextContent).Text, "42-abc") || len(got) != 3 || got[2] != "installed x" {
		t.Fatalf("propose_setup_step: %v %+v %q", err, r, got)
	}
}

// Team skills not installed here show in list_skills and load on get_skill.
func TestMCPTeamSkills(t *testing.T) {
	lib, fetched := t.TempDir(), t.TempDir()
	addSkill(t, lib, "benchmark", "local benchmark")
	addSkill(t, fetched, "profiler", "team profiler")
	svc := &Service{Sources: []Source{{"library", lib}},
		TeamList: func() []search.Skill {
			p := search.NewSkill("profiler", "team profiler", "", "infer (not linked)")
			p.Ref = "infer:profiler"
			return []search.Skill{search.NewSkill("benchmark", "team copy", "", "infer (not linked)"), p}
		},
		Fetch: func(sk search.Skill) (string, error) { return filepath.Join(fetched, sk.Name), nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := svc.New("test").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args map[string]any) string {
		t.Helper()
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("%s: %v %+v", name, err, r)
		}
		return r.Content[0].(*mcp.TextContent).Text
	}
	out := call("list_skills", map[string]any{})
	if !strings.Contains(out, "- profiler [infer (not linked)]") || strings.Contains(out, "team copy") {
		t.Fatalf("list_skills:\n%s", out)
	}
	if out = call("get_skill", map[string]any{"name": "profiler"}); !strings.Contains(out, "Steps for profiler") {
		t.Fatalf("get_skill team:\n%s", out)
	}
}
