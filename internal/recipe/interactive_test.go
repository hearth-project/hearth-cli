package recipe

import (
	"strings"
	"testing"
)

// A manual step with tty = true may name its command and environment; the
// plan offers it to agents (run_with), the key stays the one of its text.
func TestInteractiveStep(t *testing.T) {
	r, err := Parse([]byte(`name = "login"
[[step]]
type = "manual"
name = "qr-login"
text = "log in"
tty = true
run = 'tool login --key "$K"'
set = { K = "${secret:K}", https_proxy = "http://p:8080" }
done_when = "tool status"
`))
	if err != nil {
		t.Fatal(err)
	}
	p := Plan([]*Recipe{r}, PlanOptions{StateDir: t.TempDir()})
	if len(p) != 1 || p[0].RunWith == "" || len(p[0].Commands) != 1 || strings.Join(p[0].Env, ",") != "K,https_proxy" ||
		!p[0].Interactive || p[0].Check != "tool status" || p[0].Key != ManualKey("login", r.Steps[0]) {
		t.Fatalf("%+v", p)
	}
	for _, bad := range []string{
		"[[step]]\ntype = \"manual\"\ntext = \"x\"\nrun = \"y\"\n",                                        // run without tty
		"[[step]]\ntype = \"manual\"\ntext = \"x\"\ntty = true\nset = { K = \"v\" }\n",                    // set without run
		"[[step]]\ntype = \"manual\"\ntext = \"x\"\ntty = true\nrun = \"y\"\nset = { \"a-b\" = \"v\" }\n", // bad name
	} {
		if _, err := Parse([]byte("name = \"x\"\n" + bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
