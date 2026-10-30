package client

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// After the agent home changes, this person's Codex app-servers that still
// run on the shared ~/.codex or an earlier home are found; other people's,
// ones already on the new home and homes set on purpose are not.
func TestStaleAppServers(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("HEARTH_AGENTS", "codex")
	ws := filepath.Join(h, "ws")
	os.MkdirAll(ws, 0o755)
	home, err := InitWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEARTH_WORKSPACE", ws)
	const me = "dev@example.com"
	// The block first exports an earlier home, then the isolated one.
	earlier := filepath.Join(ws, ".codex")
	if err := setLocalAgentHome(home, "codex", earlier); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureIdentityRC(home, me); err != nil {
		t.Fatal(err)
	}
	own := OwnAgentHome(home, "codex")
	if err := setLocalAgentHome(home, "codex", own); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureIdentityRC(home, me); err != nil {
		t.Fatal(err)
	}
	if got := ReplacedAgentHomes(home)["CODEX_HOME"]; len(got) != 1 || got[0] != earlier {
		t.Fatalf("replaced homes %v", got)
	}

	proc := t.TempDir()
	procRoot = proc
	t.Cleanup(func() { procRoot = "/proc" })
	add := func(pid int, args []string, env ...string) {
		d := filepath.Join(proc, strconv.Itoa(pid))
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "cmdline"), []byte(strings.Join(args, "\x00")+"\x00"), 0o644)
		os.WriteFile(filepath.Join(d, "environ"), []byte(strings.Join(env, "\x00")+"\x00"), 0o644)
	}
	node := []string{"/opt/node/bin/node", "/opt/node/lib/node_modules/@openai/codex/bin/codex.js", "app-server", "--listen", "unix://"}
	vendor := []string{"/opt/node/lib/vendor/bin/codex", "-c", "x=1", "app-server", "--listen", "unix://"}
	add(101, node, "HEARTH_USER="+me, "CODEX_HOME="+filepath.Join(h, ".codex"))           // shared default: stale
	add(102, vendor, "HEARTH_USER="+me)                                                   // no CODEX_HOME = ~/.codex: stale
	add(103, vendor, "HEARTH_USER="+me, "CODEX_HOME="+earlier)                            // earlier block: stale
	add(104, vendor, "HEARTH_USER="+me, "CODEX_HOME="+own)                                // already on the new home
	add(105, vendor, "HEARTH_USER=someone@example.com", "CODEX_HOME="+h+"/.codex")        // another person
	add(106, vendor, "HEARTH_USER="+me, "CODEX_HOME=/tmp/cx")                             // set on purpose
	add(107, []string{"/usr/bin/bash", "-c", "app-server"}, "HEARTH_USER="+me)            // not codex
	add(108, []string{"/opt/node/lib/vendor/bin/codex", "exec", "hi"}, "HEARTH_USER="+me) // not a server

	var pids []int
	for _, a := range StaleAppServers(home, me) {
		pids = append(pids, a.PID)
	}
	if got := fmtInts(pids); got != "101 102 103" {
		t.Fatalf("stale %v", got)
	}
}

func fmtInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, " ")
}

func TestCodexLoginMissing(t *testing.T) {
	d := t.TempDir()
	if got := strings.Join(codexLoginMissing(d), ","); got != "auth.json,config.toml" {
		t.Fatalf("empty home: %q", got)
	}
	os.WriteFile(filepath.Join(d, "config.toml"), nil, 0o600)
	if got := strings.Join(codexLoginMissing(d), ","); got != "auth.json" {
		t.Fatalf("config only: %q", got)
	}
	os.WriteFile(filepath.Join(d, "auth.json"), []byte("{}"), 0o600)
	if got := codexLoginMissing(d); len(got) != 0 {
		t.Fatalf("logged in: %v", got)
	}
}
