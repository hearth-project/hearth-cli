package main

import "testing"

// The command handed to the user runs the login on the machine, in a
// terminal, with hearth found in ~/.local/bin.
func TestLoginCommandLine(t *testing.T) {
	t.Setenv("HEARTH_LOGIN_REMOTE_BIN", "")
	got := sshLine("gpu-a", loginScript("learned/5dc36cca78"))
	want := `hearth ssh -t gpu-a -- 'PATH="$HOME/.local/bin:$PATH"; exec hearth recipe login learned/5dc36cca78'`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := sshLine("my box", loginScript("a/b", "--check")); got != `hearth ssh -t 'my box' -- 'PATH="$HOME/.local/bin:$PATH"; exec hearth recipe login a/b --check'` {
		t.Fatalf("quoting: %s", got)
	}
	for _, k := range []string{"learned/5dc36cca78", "tools/a.b-c_d"} {
		if !stepKeyRE.MatchString(k) {
			t.Errorf("rejected %q", k)
		}
	}
	for _, k := range []string{"", "a;b", "a b", "$(x)", "a'b"} {
		if stepKeyRE.MatchString(k) {
			t.Errorf("accepted %q", k)
		}
	}
}
