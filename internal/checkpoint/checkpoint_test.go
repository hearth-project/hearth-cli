package checkpoint

import (
	"strings"
	"testing"
)

func TestParseAgents(t *testing.T) {
	codex := `{"type":"session_meta","payload":{"cwd":"/x"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>cwd</environment_context>"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the bug in main.go"}]}}
{"type":"response_item","payload":{"type":"function_call","name":"exec_command"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Fixed it."}]}}
not json
`
	turns := Parse("codex", []byte(codex))
	if len(turns) != 2 || turns[0].Text != "fix the bug in main.go" || turns[1].Role != "assistant" {
		t.Fatalf("codex: %+v", turns)
	}
	claude := `{"type":"user","message":{"role":"user","content":"add tests"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Added."},{"type":"tool_use","name":"Bash"}]}}
`
	if turns := Parse("claude", []byte(claude)); len(turns) != 2 || turns[1].Text != "Added." {
		t.Fatalf("claude: %+v", turns)
	}
	comate := `{"isAgent":false,"messages":[{"role":"user","content":"","code":"def f(): pass"},{"role":"assistant","content":"explained"}]}`
	if turns := Parse("comate", []byte(comate)); len(turns) != 2 || turns[0].Text != "def f(): pass" {
		t.Fatalf("comate: %+v", turns)
	}
	doc := Render(Source{Agent: "codex", Path: "p", Host: "h", ID: "abc"}, Parse("codex", []byte(codex)), 1)
	if !strings.Contains(doc, "## Original request\n\nfix the bug in main.go") || !strings.Contains(doc, "(1 of 2)") || !strings.Contains(doc, "Fixed it.") {
		t.Fatalf("render:\n%s", doc)
	}
}

func TestStripInjected(t *testing.T) {
	for in, want := range map[string]string{
		`<external_codex_apps_open_page>{"page_id":null}</external_codex_apps_open_page>`:       "",
		"<app-context>\nx\n</app-context>\n<skills_instructions>y</skills_instructions>\nhello": "hello",
		"<b>bold</b> is html":        "<b>bold</b> is html",
		"plain <tag> text":           "plain <tag> text",
		"<not closed and a question": "<not closed and a question",
	} {
		if got := stripInjected(in); got != want {
			t.Errorf("stripInjected(%q) = %q, want %q", in, got, want)
		}
	}
}
