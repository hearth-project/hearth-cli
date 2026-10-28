package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codexAgent = `name = "sglang-expert"
description = "分析 SGLang 源码，给出 file:line 证据"
model_reasoning_effort = "high"
developer_instructions = '''
你是 SGLang 代码专家。

- 先确认源码，再下结论。
'''
`

const claudeAgent = `---
name: reviewer
description: Reviews diffs
tools: Read, Grep
model: sonnet
---
You review code carefully.
`

func write(t *testing.T, p, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAgentFormatsRoundTrip(t *testing.T) {
	a, err := ParseCodexAgent([]byte(codexAgent))
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "sglang-expert" || !strings.Contains(a.Instructions, "先确认源码") || a.Codex["model_reasoning_effort"] != "high" {
		t.Fatalf("parsed %+v", a)
	}
	canon, _ := a.Format()
	b, err := ParseAgent(canon)
	if err != nil || b.Instructions != a.Instructions || b.Codex["model_reasoning_effort"] != "high" {
		t.Fatalf("canonical round trip: %v %+v", err, b)
	}
	toml, err := RenderCodexAgent(b, codexHeader)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseCodexAgent(toml)
	if err != nil || c.Instructions != a.Instructions || c.Description != a.Description || c.Codex["model_reasoning_effort"] != "high" {
		t.Fatalf("codex round trip: %v\n%s", err, toml)
	}
	// Instructions that cannot be a literal string fall back to escaping.
	c.Instructions = "has ''' quotes and \r carriage return\n"
	if out, err := RenderCodexAgent(c, ""); err != nil {
		t.Fatal(err)
	} else if d, _ := ParseCodexAgent(out); d.Instructions != c.Instructions {
		t.Fatalf("escaped instructions changed: %q", d.Instructions)
	}

	cl, err := ParseClaudeAgent([]byte(claudeAgent))
	if err != nil || cl.Claude["tools"] != "Read, Grep" || cl.Instructions != "You review code carefully.\n" {
		t.Fatalf("claude parse: %v %+v", err, cl)
	}
	md, _ := RenderClaudeAgent(cl, "")
	if back, err := ParseClaudeAgent(md); err != nil || back.Claude["model"] != "sonnet" || back.Instructions != cl.Instructions {
		t.Fatalf("claude round trip: %v\n%s", err, md)
	}
	for _, bad := range []string{"../x", "A", "", "a/b", "x..y"} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q) = true", bad)
		}
	}
}

type fixture struct {
	lib            Library
	codex, claude  string
	manifest, back string
}

func newFixture(t *testing.T) *fixture {
	d := t.TempDir()
	f := &fixture{lib: Library{Root: filepath.Join(d, "lib")}, codex: filepath.Join(d, "codex"), claude: filepath.Join(d, "claude"),
		manifest: filepath.Join(d, "render.json"), back: filepath.Join(d, "backup")}
	write(t, filepath.Join(f.lib.Root, "skills/bench/SKILL.md"), "---\nname: bench\ndescription: run benchmarks\n---\nbody\n")
	a, _ := ParseCodexAgent([]byte(codexAgent))
	canon, _ := a.Format()
	write(t, filepath.Join(f.lib.Root, "agents/sglang-expert.md"), string(canon))
	write(t, f.lib.InstructionsFile(), "Be concise.\n")
	return f
}

func (f *fixture) render(t *testing.T, adopt bool) map[string]string {
	t.Helper()
	acts, err := Render(f.lib, []Target{CodexTarget(f.codex), ClaudeTarget(f.claude)},
		Options{Manifest: f.manifest, BackupDir: f.back, Adopt: adopt})
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]string{}
	for _, a := range acts {
		rel := strings.TrimPrefix(strings.TrimPrefix(a.Path, filepath.Dir(f.codex)), "/")
		if a.Op != "warn" {
			ops[rel] = a.Op
		}
	}
	return ops
}

func TestRenderLifecycle(t *testing.T) {
	f := newFixture(t)
	ops := f.render(t, false)
	for _, p := range []string{"codex/skills/bench", "claude/skills/bench", "codex/agents/sglang-expert.toml",
		"claude/agents/sglang-expert.md", "codex/AGENTS.md", "claude/CLAUDE.md"} {
		if ops[p] != "create" {
			t.Fatalf("%s: %q (all: %v)", p, ops[p], ops)
		}
	}
	if dst, _ := os.Readlink(filepath.Join(f.codex, "skills/bench")); dst != filepath.Join(f.lib.Root, "skills/bench") {
		t.Fatalf("skill link -> %q", dst)
	}
	if !strings.Contains(read(t, filepath.Join(f.claude, "CLAUDE.md")), "@"+f.lib.InstructionsFile()) {
		t.Fatal("CLAUDE.md does not import library instructions")
	}
	if _, err := ParseCodexAgent([]byte(read(t, filepath.Join(f.codex, "agents/sglang-expert.toml")))); err != nil {
		t.Fatalf("rendered codex agent invalid: %v", err)
	}
	if !strings.Contains(read(t, filepath.Join(f.claude, "agents/sglang-expert.md")), "先确认源码") {
		t.Fatal("claude agent lacks instructions")
	}

	// Idempotent.
	for p, op := range f.render(t, false) {
		if op != "unchanged" {
			t.Fatalf("second render: %s %s", p, op)
		}
	}

	// Library edit propagates; manual edit of a generated file is protected.
	a, _ := ParseAgent([]byte(read(t, filepath.Join(f.lib.Root, "agents/sglang-expert.md"))))
	a.Description = "updated"
	canon, _ := a.Format()
	write(t, filepath.Join(f.lib.Root, "agents/sglang-expert.md"), string(canon))
	write(t, filepath.Join(f.claude, "agents/sglang-expert.md"), "hand edited\n")
	ops = f.render(t, false)
	if ops["codex/agents/sglang-expert.toml"] != "update" || ops["claude/agents/sglang-expert.md"] != "skip" {
		t.Fatalf("edit handling: %v", ops)
	}
	if read(t, filepath.Join(f.claude, "agents/sglang-expert.md")) != "hand edited\n" {
		t.Fatal("manual edit overwritten")
	}

	// Removing a skill from the library removes our symlinks only.
	os.RemoveAll(filepath.Join(f.lib.Root, "skills/bench"))
	ops = f.render(t, false)
	if ops["codex/skills/bench"] != "remove" {
		t.Fatalf("removal: %v", ops)
	}
	if _, err := os.Lstat(filepath.Join(f.codex, "skills/bench")); !os.IsNotExist(err) {
		t.Fatal("stale symlink left behind")
	}
}

func TestUnmanagedPathsNeedAdopt(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.codex, "skills/bench/SKILL.md"), "my own\n")
	// The agent's own copy stays and counts as present (one copy is enough).
	if ops := f.render(t, false); ops["codex/skills/bench"] != "present" {
		t.Fatalf("unmanaged skill not kept as present: %v", ops)
	}
	if read(t, filepath.Join(f.codex, "skills/bench/SKILL.md")) != "my own\n" {
		t.Fatal("unmanaged skill touched")
	}
	if ops := f.render(t, true); ops["codex/skills/bench"] != "adopt" {
		t.Fatalf("adopt: %v", ops)
	}
	matches, _ := filepath.Glob(filepath.Join(f.back, "*", "*codex__skills__bench", "SKILL.md"))
	if len(matches) != 1 || read(t, matches[0]) != "my own\n" {
		t.Fatalf("backup missing: %v", matches)
	}
}

func TestImportThenAdopt(t *testing.T) {
	d := t.TempDir()
	lib := Library{Root: filepath.Join(d, "lib")}
	codex, claude := filepath.Join(d, "codex"), filepath.Join(d, "claude")
	write(t, filepath.Join(codex, "skills/bench/SKILL.md"), "---\nname: bench\n---\nx\n")
	write(t, filepath.Join(codex, "skills/bench/scripts/run.sh"), "echo hi\n")
	write(t, filepath.Join(codex, "skills/.system/imagegen/SKILL.md"), "system\n")
	write(t, filepath.Join(codex, "agents/sglang-expert.toml"), codexAgent)
	write(t, filepath.Join(claude, "agents/reviewer.md"), claudeAgent)
	mf := filepath.Join(d, "render.json")

	if _, err := Import(lib, CodexTarget(codex), mf, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(lib, ClaudeTarget(claude), mf, false); err != nil {
		t.Fatal(err)
	}
	c, err := lib.Load()
	if err != nil || len(c.Skills) != 1 || len(c.Agents) != 2 || len(c.Problems) != 0 {
		t.Fatalf("library after import: %+v %v", c, err)
	}
	if read(t, filepath.Join(lib.SkillsDir(), "bench/scripts/run.sh")) != "echo hi\n" {
		t.Fatal("skill files not copied")
	}
	// Cross-agent: the Claude agent is now available to Codex and vice versa.
	if _, err := Render(lib, []Target{CodexTarget(codex), ClaudeTarget(claude)}, Options{Manifest: mf, BackupDir: filepath.Join(d, "bak"), Adopt: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCodexAgent([]byte(read(t, filepath.Join(codex, "agents/reviewer.toml")))); err != nil {
		t.Fatalf("claude agent not rendered for codex: %v", err)
	}
	if _, err := ParseClaudeAgent([]byte(read(t, filepath.Join(claude, "agents/sglang-expert.md")))); err != nil {
		t.Fatalf("codex agent not rendered for claude: %v", err)
	}
	// Re-import skips everything hearth now manages.
	acts, _ := Import(lib, CodexTarget(codex), mf, false)
	for _, a := range acts {
		if a.Op == "import" {
			t.Fatalf("re-imported managed path %s", a.Path)
		}
	}
}

func TestResidentListLimitsRenderedSkills(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.lib.Root, "skills/rare/SKILL.md"), "---\nname: rare\n---\nx\n")
	f.render(t, false)
	if _, err := os.Lstat(filepath.Join(f.codex, "skills/rare")); err != nil {
		t.Fatal("without resident.txt every skill is rendered")
	}
	write(t, f.lib.ResidentFile(), "# keep only the hot ones\nbench\n")
	ops := f.render(t, false)
	if ops["codex/skills/rare"] != "remove" || ops["codex/skills/bench"] != "unchanged" {
		t.Fatalf("resident filter: %v", ops)
	}
}

// A skill the agent loads from elsewhere (the project's .codex/skills) is
// not linked into the agent home again.
func TestPresentElsewhereIsNotLinked(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(t.TempDir(), "bench")
	write(t, filepath.Join(other, "SKILL.md"), "project copy\n")
	acts, err := Render(f.lib, []Target{CodexTarget(f.codex), ClaudeTarget(f.claude)}, Options{
		Manifest: f.manifest, BackupDir: f.back,
		Present: func(tg Target, name string) string {
			if tg.Name == "codex" && name == "bench" {
				return other
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(f.codex, "skills/bench")); err == nil {
		t.Fatal("linked although the project has it")
	}
	if _, err := os.Lstat(filepath.Join(f.claude, "skills/bench")); err != nil {
		t.Fatal("claude does not see the project dir and needs the link")
	}
	found := false
	for _, a := range acts {
		found = found || (a.Op == "present" && a.Path == other)
	}
	if !found {
		t.Fatalf("no present action: %+v", acts)
	}
}
