package client

import "testing"

func TestAgentIntegrationRegistry(t *testing.T) {
	for _, name := range []string{"codex", "claude", "comate"} {
		in, ok := Integration(name)
		if !ok || in.Name() != name || !in.Capabilities().Sessions {
			t.Fatalf("%s: %+v %v", name, in, ok)
		}
	}
	codex, _ := Integration("codex")
	if !codex.Capabilities().Threads || !codex.Capabilities().LiveUI || !codex.Capabilities().DeleteSync {
		t.Fatalf("codex capabilities: %+v", codex.Capabilities())
	}
	if _, ok := Integration("unknown"); ok {
		t.Fatal("unknown agent has an integration")
	}
}

func TestConversationKeyGroupsCodexThread(t *testing.T) {
	id := "01a162cf-d71d-7952-b0f0-10083a5ddb85"
	a := "sessions/2026/10/22/rollout-2026-10-22T16-52-30-" + id + ".jsonl"
	b := "sessions/2026/10/22/rollout-2026-10-22T17-52-30-" + id + "_01a163a5-aaa7-7271-a561-dc6788338284.jsonl"
	if ConversationKey("codex", a, "sid-a") != ConversationKey("codex", b, "sid-b") {
		t.Fatal("codex thread segments got different conversation keys")
	}
	if ConversationKey("claude", a, "sid-a") != "sid-a" {
		t.Fatal("single-file agent should use the session id")
	}
}
