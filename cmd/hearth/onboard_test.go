package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/client"
)

// moveEnv: ~/.hearth logged in as alice, and a fresh workspace named name.
func moveEnv(t *testing.T, name string) (home, ws string) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	for _, v := range []string{"HEARTH_IDENTITY", "COMATE_USERNAME", "SYS_USER_NAME"} {
		t.Setenv(v, "")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "none"))
	if err := client.SaveConfig(client.DefaultHome(), &client.Config{Email: "alice@example.com", Server: "https://x"}); err != nil {
		t.Fatal(err)
	}
	ws = filepath.Join(t.TempDir(), name)
	os.MkdirAll(ws, 0o700)
	t.Setenv("HEARTH_WORKSPACE", ws)
	home, err := client.InitWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	return home, ws
}

func comateLogin(t *testing.T, user string) {
	p := filepath.Join(os.Getenv("HOME"), ".comate", "login-user", user)
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("Bearer-x"), 0o600)
}

// The workspace's identity is ~/.hearth's account: move, do not log in again.
func TestMoveLoginStepSameUser(t *testing.T) {
	home, ws := moveEnv(t, "alice")
	comateLogin(t, "alice")
	n, ok := moveLoginStep(home, ws)
	if !ok || !n.mine || n.login != "alice@example.com" || !strings.HasPrefix(n.step.Command, "hearth init --move-login "+ws) {
		t.Fatalf("step %+v ok=%v", n, ok)
	}
}

// Another person's workspace on the shared account: never offer their login.
func TestMoveLoginStepOtherUser(t *testing.T) {
	home, ws := moveEnv(t, "bob")
	comateLogin(t, "alice")
	comateLogin(t, "bob")
	if n, ok := moveLoginStep(home, ws); ok {
		t.Fatalf("offered alice's login to bob: %+v", n)
	}
}

// Nothing says whose workspace it is: offer the move, but ask.
func TestMoveLoginStepUnknownUser(t *testing.T) {
	home, ws := moveEnv(t, "work")
	n, ok := moveLoginStep(home, ws)
	if !ok || n.mine || n.step.AskUser == "" {
		t.Fatalf("step %+v ok=%v", n, ok)
	}
}

// Several people known on a shared account: list them, pick by number or
// type an email.
func TestChooseIdentity(t *testing.T) {
	cs := []client.LocalIdentity{{Username: "alice", Email: "alice@example.com", Source: "token a"}, {Username: "bob", Source: "comate login bob"}}
	answers := func(as ...string) func(string) (string, bool) {
		return func(string) (string, bool) {
			if len(as) == 0 {
				return "", false
			}
			a := as[0]
			as = as[1:]
			return a, true
		}
	}
	var out strings.Builder
	if e, err := chooseIdentity(cs, answers("1"), &out); err != nil || e != "alice@example.com" {
		t.Fatalf("pick 1: %q %v", e, err)
	}
	if !strings.Contains(out.String(), "1) alice@example.com") || !strings.Contains(out.String(), "2) bob  (comate login bob)") {
		t.Fatalf("list:\n%s", out.String())
	}
	if e, _ := chooseIdentity(cs, answers("2", "bob@corp.example"), io.Discard); e != "bob@corp.example" {
		t.Fatalf("pick 2 without a domain: %q", e)
	}
	if e, _ := chooseIdentity(cs, answers("9", "carol@example.com"), io.Discard); e != "carol@example.com" {
		t.Fatalf("typed email: %q", e)
	}
	if _, err := chooseIdentity(cs, answers(), io.Discard); err == nil {
		t.Fatal("no answer accepted")
	}
}
