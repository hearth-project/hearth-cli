package recipe

import (
	"testing"

	"github.com/hearth-project/hearth-cli/internal/machine"
)

func TestPlan(t *testing.T) {
	r, err := Parse([]byte(`name = "stack"
[[step]]
type = "manual"
name = "node"
text = "install node"
done_when = "node --version"
[[step]]
type = "file"
secret = "x"
to = ["~/.x"]
[[step]]
type = "run"
name = "import"
when_secret = "cfg"
run = "import it"
unless = "check it"
[[step]]
type = "tool"
name = "mac-only"
npm = "x@1"
when = { os = "darwin" }
[[step]]
type = "check"
name = "works"
run = "true"
`))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	opt := PlanOptions{StateDir: dir, Machine: &machine.Facts{OS: "linux", Arch: "x86_64"},
		Secret: func(n string) (bool, error) { return n == "cfg", nil }}
	p := Plan([]*Recipe{r}, opt)
	var got []string
	for _, s := range p {
		got = append(got, s.Name+":"+s.Status)
	}
	want := []string{"node:todo", "import:todo", "mac-only:skipped", "works:todo"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v, want %v", got, want)
		}
	}
	if p[1].Check != "check it" || p[1].Commands[0] != "import it" || p[0].Check != "node --version" {
		t.Fatalf("%+v", p[:2])
	}
	if err := MarkDone(dir, p[0].Key, p[3].Key); err != nil {
		t.Fatal(err)
	}
	p = Plan([]*Recipe{r}, opt)
	if p[0].Status != "done" || p[3].Status != "done" || p[1].Status != "todo" {
		t.Fatalf("after done %+v", p)
	}
	opt.Secret = func(string) (bool, error) { return false, nil }
	if p = Plan([]*Recipe{r}, opt); p[1].Status != "skipped" {
		t.Fatalf("without the secret %+v", p[1])
	}
}
