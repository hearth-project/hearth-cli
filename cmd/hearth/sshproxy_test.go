package main

import (
	"errors"
	"net"
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

// Only a machine that does not answer from here and holds a relay connection
// gets a ProxyCommand; everything else keeps its plain entry.
func TestSSHConfigRoutes(t *testing.T) {
	home := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	live := ln.Addr().(*net.TCPAddr)
	// A port nobody listens on.
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadPort := dead.Addr().(*net.TCPAddr).Port
	dead.Close()

	eps := []client.SSHEndpoint{
		{DeviceID: "direct", Address: "127.0.0.1", Port: live.Port},
		{DeviceID: "viaRelay", Address: "127.0.0.1", Port: deadPort},
		{DeviceID: "nowhere", Address: "127.0.0.1", Port: deadPort},
		{DeviceID: "self", Address: "127.0.0.1", Port: deadPort},
		{DeviceID: "both", Address: "127.0.0.1", Addresses: []string{"127.0.0.2", "127.0.0.1"}, Port: live.Port},
	}
	ids := func() ([]string, error) { return []string{"direct", "viaRelay", "self", "both"}, nil }
	r := sshConfigRoutes(home, "self", eps, ids)
	for i, want := range []bool{false, true, false, false, false} {
		if got := r[i].proxy != ""; got != want {
			t.Errorf("%s: ProxyCommand = %q, want one: %v", eps[i].DeviceID, r[i].proxy, want)
		}
	}
	if !strings.Contains(r[1].proxy, "--device 'viaRelay'") {
		t.Errorf("proxy = %q", r[1].proxy)
	}
	if r[4].host != "127.0.0.1" {
		t.Errorf("host of a machine with several addresses = %q", r[4].host)
	}

	// The relay's list is unknown: nothing changes.
	for i, x := range sshConfigRoutes(home, "self", eps, func() ([]string, error) { return nil, errors.New("down") }) {
		if x.proxy != "" {
			t.Errorf("%s: ProxyCommand with the relay list unknown", eps[i].DeviceID)
		}
	}

	// [ssh] relay = false: never.
	off := false
	cfg, _ := client.UserConfig(home)
	cfg.SSH = &library.SSHConfig{Relay: &off}
	if err := client.SaveUserConfig(home, cfg); err != nil {
		t.Fatal(err)
	}
	for i, x := range sshConfigRoutes(home, "self", eps, ids) {
		if x.proxy != "" {
			t.Errorf("%s: ProxyCommand with [ssh] relay = false", eps[i].DeviceID)
		}
	}
}
