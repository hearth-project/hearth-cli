package client

import (
	"github.com/hearth-project/hearth-cli/internal/memory"

	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Conversations of one machine (with their cards), facts and hook output
// reach another machine's local index.
func TestMemoryAcrossMachines(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	p := filepath.Join(a.codex, "sessions/2026/10/28/rollout-2026-10-28T10-00-00-0199eeee-0000-7000-8000-000000000001.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o700)
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	line := func(v any) string { j, _ := json.Marshal(v); return string(j) }
	args, _ := json.Marshal(map[string]any{"cmd": "python -m pytest tests/test_nccl.py"})
	lines := []string{
		line(map[string]any{"timestamp": ts, "type": "session_meta", "payload": map[string]any{"id": "0199eeee-0000-7000-8000-000000000001", "cwd": "/ws/sglang"}}),
		line(map[string]any{"timestamp": ts, "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "the all-reduce hangs on the H800 cluster, find out why"}}}}),
		line(map[string]any{"timestamp": ts, "type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "arguments": string(args)}}),
		line(map[string]any{"timestamp": ts, "type": "response_item", "payload": map[string]any{"type": "function_call_output", "output": "Process exited with code 1\nRuntimeError: NCCL error: unhandled system error"}}),
		line(map[string]any{"timestamp": ts, "type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "input": "*** Begin Patch\n*** Update File: python/launch.sh\n@@\n*** End Patch"}}),
		line(map[string]any{"timestamp": ts, "type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Fixed: the cluster needs NCCL_IB_HCA=mlx5 so NCCL uses the InfiniBand cards; launch.sh now exports it."}}}}),
	}
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	a.push(t)
	if _, err := a.Remember("answer in Chinese", "", "user"); err != nil {
		t.Fatal(err)
	}

	st, err := b.IndexMemory()
	if err != nil || st.Conversations != 1 || st.Facts != 1 {
		t.Fatalf("index %+v %v", st, err)
	}
	hits, err := Recall(b.Home, "nccl hang h800", "/ws/sglang/python", 3)
	if err != nil || len(hits) == 0 || !strings.HasPrefix(hits[0].ID, "s:") {
		t.Fatalf("recall %+v %v", hits, err)
	}
	for _, want := range []string{"NCCL_IB_HCA=mlx5", "pytest", "launch.sh", "unhandled system error"} {
		if !strings.Contains(hits[0].Body, want) {
			t.Fatalf("card lacks %q:\n%s", want, hits[0].Body)
		}
	}
	out, err := Hook(b.Home, strings.NewReader(`{"hook_event_name":"UserPromptSubmit","prompt":"NCCL all-reduce hangs again on H800","cwd":"/ws/sglang"}`))
	if err != nil || !strings.Contains(out, "additionalContext") || !strings.Contains(out, "mlx5") {
		t.Fatalf("hook %s %v", out, err)
	}
	// The conversation itself is not offered back to it.
	if out, _ = Hook(b.Home, strings.NewReader(`{"hook_event_name":"UserPromptSubmit","prompt":"NCCL all-reduce hangs again on H800","session_id":"0199eeee-0000-7000-8000-000000000001"}`)); out != "" {
		t.Fatalf("own conversation injected: %s", out)
	}
	if out, _ = Hook(b.Home, strings.NewReader(`{"hook_event_name":"UserPromptSubmit","prompt":"write a haiku about autumn leaves"}`)); out != "" {
		t.Fatalf("unrelated prompt got memories: %s", out)
	}
	out, _ = Hook(b.Home, strings.NewReader(`{"hook_event_name":"SessionStart","cwd":"/x"}`))
	if !strings.Contains(out, "answer in Chinese") {
		t.Fatalf("session start: %s", out)
	}
	// Forgetting removes the fact everywhere; credentials are refused.
	fs, _ := b.Facts()
	if err := b.ForgetFact(fs[0].ID); err != nil {
		t.Fatal(err)
	}
	if st, _ = a.IndexMemory(); st.Facts != 0 {
		t.Fatalf("fact still there: %+v", st)
	}
	if _, err := a.Remember("token = abcdefghijklmnopqrstuvwx1234", "", "user"); err == nil {
		t.Fatal("credential remembered")
	}
}

// Hearth's hook entries are added next to the user's own and removed
// again without touching anything else.
func TestMemoryHooksFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	other := `{"model":"x","hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"/usr/bin/other"}]}]}}`
	os.WriteFile(p, []byte(other), 0o600)
	cmd := hookCommand("", "/bin/hearth")
	for i := 0; i < 2; i++ { // idempotent
		if _, err := editHooks(p, cmd, true, false); err != nil {
			t.Fatal(err)
		}
	}
	var doc map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &doc)
	hooks := doc["hooks"].(map[string]any)
	if doc["model"] != "x" || len(hooks["UserPromptSubmit"].([]any)) != 2 || len(hooks["SessionStart"].([]any)) != 1 {
		t.Fatalf("after install:\n%s", b)
	}
	if !strings.Contains(string(b), "\"'/bin/hearth' recall --hook\"") {
		t.Fatalf("command:\n%s", b)
	}
	// A recorded copy with another machine's hook path is not "changed".
	other2 := strings.ReplaceAll(string(b), "/bin/hearth", "/elsewhere/hearth")
	if string(withoutOwn([]byte(other2))) != string(withoutOwn(b)) {
		t.Fatal("own hooks make the file look changed")
	}
	if op, err := editHooks(p, cmd, false, false); err != nil || op != "removed" {
		t.Fatalf("remove: %s %v", op, err)
	}
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), "recall --hook") || !strings.Contains(string(b), "/usr/bin/other") || strings.Contains(string(b), "SessionStart") {
		t.Fatalf("after removal:\n%s", b)
	}
}

// One Codex home shared by several people (one Unix account): the hook
// answers with the facts of the workspace the session runs in, whoever
// registered it, and with nobody's in a workspace without a login.
func TestHookAnswersForTheSessionsWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HEARTH_WORKSPACE", "")
	fact := func(home, body string) {
		x, err := memory.Open(memory.Path(home))
		if err != nil {
			t.Fatal(err)
		}
		defer x.Close()
		if err := x.Apply([]memory.Doc{{ID: "f:1", Kind: "fact", Title: body, Body: body, At: 1}}, map[string]string{"f:1": "h"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	homes := map[string]string{}
	for _, u := range []string{"alice", "bob", "carol"} {
		os.MkdirAll(filepath.Join(root, u), 0o700)
		h, err := InitWorkspace(filepath.Join(root, u))
		if err != nil {
			t.Fatal(err)
		}
		homes[u] = h
	}
	fact(homes["alice"], "alice prefers tabs")
	fact(homes["bob"], "bob prefers spaces")
	fact(DefaultHome(), "root owner prefers vim")

	start := func(cwd string) string {
		out, _ := Hook(homes["alice"], strings.NewReader(`{"hook_event_name":"SessionStart","cwd":"`+cwd+`"}`))
		return out
	}
	if out := start(filepath.Join(root, "bob", "repo")); !strings.Contains(out, "bob prefers spaces") || strings.Contains(out, "alice") {
		t.Fatalf("bob's session got: %s", out)
	}
	if out := start(filepath.Join(root, "alice")); !strings.Contains(out, "alice prefers tabs") {
		t.Fatalf("alice's session got: %s", out)
	}
	if out := start(filepath.Join(root, "carol")); out != "" {
		t.Fatalf("carol (no memory) got: %s", out)
	}
	if out := start("/elsewhere"); !strings.Contains(out, "root owner prefers vim") {
		t.Fatalf("session outside workspaces got: %s", out)
	}
	if c := hookCommand(homes["alice"], "/bin/hearth"); strings.Contains(c, "HEARTH_HOME") {
		t.Fatalf("hook names a workspace home: %s", c)
	}
	if c := hookCommand("/opt/h", "/bin/hearth"); !strings.Contains(c, "HEARTH_HOME='/opt/h'") {
		t.Fatalf("hook drops a custom home: %s", c)
	}
}

// Key memory: entries are capped one by one and in all, replaces rewrites
// and merges, and session start gives every entry in force with its id,
// even when an older one is too long (it used to empty the whole block).
func TestKeyMemory(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	if _, err := a.Remember(strings.Repeat("x", FactLimit+1), "", "user"); err == nil {
		t.Fatal("over-long entry stored")
	}
	f1, err := a.Remember("answer in Chinese", "", "user")
	if err != nil {
		t.Fatal(err)
	}
	f2, err := a.Remember("run  pre-commit\nbefore every push", "/ws/repo", "agent")
	if err != nil || f2.Text != "run pre-commit before every push" {
		t.Fatalf("%+v %v", f2, err)
	}
	// Merge both into one: the old ones go.
	m, err := a.Remember("answer in Chinese; run pre-commit before every push", "", "agent", "f:"+f1.ID, f2.ID)
	if err != nil {
		t.Fatal(err)
	}
	fs, _ := a.Facts()
	if len(fs) != 1 || fs[0].ID != m.ID {
		t.Fatalf("after merge %+v", fs)
	}
	if _, err := a.Remember("x", "", "user", "f:nope"); err == nil {
		t.Fatal("replaced a missing entry")
	}
	// Fill to the limit: the next one is refused unless it replaces.
	for KeyMemorySize(fs)+FactLimit <= KeyMemoryLimit {
		if _, err := a.Remember(strings.Repeat("y", FactLimit-10)+" filler", "", "user"); err != nil {
			t.Fatal(err)
		}
		fs, _ = a.Facts()
	}
	if _, err := a.Remember(strings.Repeat("z", FactLimit), "", "user"); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("full: %v", err)
	}
	if _, err := a.Remember("short again", "", "user", fs[0].ID); err != nil {
		t.Fatalf("replace when full: %v", err)
	}

	// Session start: a legacy entry longer than the limit is cut, not
	// dropping the rest; scoped entries only under their directory.
	docs := []memory.Doc{
		{ID: "f:old", Kind: "fact", Body: strings.Repeat("长", 1800)},
		{ID: "f:zh", Kind: "fact", Body: "回复用中文"},
		{ID: "f:repo", Kind: "fact", Body: "repo rule", Cwd: "/ws/repo"},
	}
	s := KeyMemoryContext(docs, "/elsewhere")
	if !strings.Contains(s, "[f:zh] 回复用中文") || !strings.Contains(s, "[f:old]") || !strings.Contains(s, "too long") || strings.Contains(s, "repo rule") || !utf8.ValidString(s) {
		t.Fatalf("context:\n%s", s)
	}
	if s := KeyMemoryContext(docs, "/ws/repo/sub"); !strings.Contains(s, "repo rule") {
		t.Fatalf("scoped:\n%s", s)
	}
	if s := KeyMemoryContext(docs, "/x"); !strings.Contains(s, "% full") {
		t.Fatalf("no tidy hint at 5.4 KB:\n%s", s[len(s)-200:])
	}
	if KeyMemoryContext(nil, "/x") != "" {
		t.Fatal("empty memory gave context")
	}
}

// Comate's hook file gets a matcher and the plain-text command; the
// plain answer is the context itself.
func TestComateHook(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hooks.json")
	if _, err := editHooks(p, hookCommand("", "/bin/hearth")+" --plain", true, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"matcher": "*"`) || !strings.Contains(string(b), "recall --hook --plain") {
		t.Fatalf("%s", b)
	}
	en := newEnv(t)
	a := en.machine("a")
	if _, err := a.Remember("answer in Chinese", "", "user"); err != nil {
		t.Fatal(err)
	}
	out, err := HookPlain(a.Home, strings.NewReader(`{"hook_event_name":"SessionStart","cwd":"/x"}`))
	if err != nil || !strings.HasPrefix(out, "Key memory") || strings.Contains(out, "hookSpecificOutput") {
		t.Fatalf("%q %v", out, err)
	}
}
