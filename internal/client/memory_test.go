package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	cmd := hookCommand("/h", "/bin/hearth")
	for i := 0; i < 2; i++ { // idempotent
		if _, err := editHooks(p, cmd, true); err != nil {
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
	if !strings.Contains(string(b), "HEARTH_HOME='/h' '/bin/hearth' recall --hook") {
		t.Fatalf("command:\n%s", b)
	}
	// A recorded copy with another machine's hook path is not "changed".
	other2 := strings.ReplaceAll(string(b), "/bin/hearth", "/elsewhere/hearth")
	if string(withoutOwn([]byte(other2))) != string(withoutOwn(b)) {
		t.Fatal("own hooks make the file look changed")
	}
	if op, err := editHooks(p, cmd, false); err != nil || op != "removed" {
		t.Fatalf("remove: %s %v", op, err)
	}
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), "recall --hook") || !strings.Contains(string(b), "/usr/bin/other") || strings.Contains(string(b), "SessionStart") {
		t.Fatalf("after removal:\n%s", b)
	}
}
