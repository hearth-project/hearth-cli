package main

import (
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/library"
)

func TestRelayProxyCommand(t *testing.T) {
	home := t.TempDir()
	cmd := relayProxyCommand(home, "89a34b1e2df8fcd0", false)
	// ssh runs "exec <ProxyCommand>": an environment prefix must go through env.
	if !strings.HasPrefix(cmd, "env HEARTH_HOME='"+home+"' '") || !strings.Contains(cmd, "' ssh-proxy --device '89a34b1e2df8fcd0' %h %p") || strings.Contains(cmd, "--relay-only") {
		t.Fatalf("command = %q", cmd)
	}
	if only := relayProxyCommand(home, "x", true); !strings.Contains(only, "--relay-only") {
		t.Fatalf("relay-only = %q", only)
	}
	if got := relayProxyCommand(home, "", false); got != "" {
		t.Fatalf("no device id: %q", got)
	}
	// A home with a quote in its name stays one word for the shell.
	if got := relayProxyCommand("/tmp/it's here", "x", false); !strings.Contains(got, `HEARTH_HOME='/tmp/it'\''s here'`) {
		t.Fatalf("quoting: %q", got)
	}
}

func TestRelayProxyCommandOff(t *testing.T) {
	home := t.TempDir()
	off := false
	cfg, _ := client.UserConfig(home)
	cfg.SSH = &library.SSHConfig{Relay: &off}
	if err := client.SaveUserConfig(home, cfg); err != nil {
		t.Fatal(err)
	}
	if got := relayProxyCommand(home, "x", false); got != "" {
		t.Fatalf("[ssh] relay = false still gives a ProxyCommand: %q", got)
	}
}

func TestShQuote(t *testing.T) {
	for in, want := range map[string]string{"plain": "'plain'", "a b": "'a b'", "it's": `'it'\''s'`, "": "''"} {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
