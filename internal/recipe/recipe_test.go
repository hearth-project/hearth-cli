package recipe

import (
	"github.com/hearth-project/hearth-cli/internal/machine"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeHost struct {
	secrets map[string][]byte
	lib     map[string][]byte
}

func (h *fakeHost) Secret(n string) ([]byte, bool, error) { v, ok := h.secrets[n]; return v, ok, nil }
func (h *fakeHost) SetSecret(n string, v []byte) error    { h.secrets[n] = v; return nil }
func (h *fakeHost) LibraryFile(rel string) ([]byte, error) {
	if v, ok := h.lib[rel]; ok {
		return v, nil
	}
	return nil, os.ErrNotExist
}

const sshRecipe = `
name = "ssh-identities"
description = "test"

[[step]]
type = "file"
secret = "id_rsa"
to = ["~/.hearth/ssh/id_rsa", "~/.ssh/id_rsa"]

[[step]]
type = "file"
library = "files/ssh_config"
to = ["~/.hearth/ssh/config"]
policy = "warn"

[[step]]
type = "env"
scope = ["~/work"]
set = { GIT_SSH_COMMAND = "ssh -F ~/.hearth/ssh/config" }
alias = { sshx = "ssh -F ~/.hearth/ssh/config" }

[[step]]
type = "env"
set = { EDITOR = "vim" }

[[step]]
type = "check"
name = "config has gitsrv port"
run = "grep -c 'Port 8235' ~/.hearth/ssh/config"
expect = "^1"

[[step]]
type = "manual"
text = "register the public key in the git server"
done_when = "test -f ~/.registered"
`

func setup(t *testing.T) (home string, h *fakeHost, recipes []*Recipe) {
	home = t.TempDir()
	t.Setenv("HOME", home)
	r, err := Parse([]byte(sshRecipe))
	if err != nil {
		t.Fatal(err)
	}
	h = &fakeHost{secrets: map[string][]byte{"id_rsa": []byte("PRIVATE")}, lib: map[string][]byte{"files/ssh_config": []byte("Host gitsrv\n  Port 8235\n")}}
	return home, h, []*Recipe{r}
}

func ops(res []Result) map[string]string {
	m := map[string]string{}
	for _, r := range res {
		m[r.Step] = r.Op
	}
	return m
}

func TestValidation(t *testing.T) {
	bad := map[string]string{
		"shared file":   "name='x'\n[[step]]\ntype='file'\nsecret='a'\nto=['~/.ssh/config']\n",
		"relative to":   "name='x'\n[[step]]\ntype='file'\nsecret='a'\nto=['x/y']\n",
		"both sources":  "name='x'\n[[step]]\ntype='file'\nsecret='a'\nlibrary='b'\nto=['~/a']\n",
		"lib traversal": "name='x'\n[[step]]\ntype='file'\nlibrary='../../etc/passwd'\nto=['~/a']\n",
		"unknown type":  "name='x'\n[[step]]\ntype='service'\n",
		"unknown field": "name='x'\n[[step]]\ntype='run'\nrun='true'\nsudo=true\n",
		"bad env name":  "name='x'\n[[step]]\ntype='env'\nset={'A B'='1'}\n",
		"bad mode":      "name='x'\n[[step]]\ntype='file'\nsecret='a'\nto=['~/a']\nmode='999'\n",
	}
	for name, src := range bad {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Parse([]byte("name='x'\n[[step]]\ntype='file'\nsecret='a'\nto=['~/.ssh/config']\nshared_ok=true\n")); err != nil {
		t.Errorf("shared_ok rejected: %v", err)
	}
}

func TestApplyIdempotentAndApprovals(t *testing.T) {
	home, h, rs := setup(t)
	state := filepath.Join(home, ".hearth")
	opt := Options{Mode: Apply, StateDir: state}

	res, err := Run(rs, h, opt)
	if err != nil {
		t.Fatal(err)
	}
	o := ops(res)
	if o["1:file ~/.hearth/ssh/id_rsa,~/.ssh/id_rsa"] != "applied" || o["2:file ~/.hearth/ssh/config"] != "applied" {
		t.Fatalf("files: %v", o)
	}
	if o["5:check config has gitsrv port"] != "needs-approval" || o["6:manual register the public key in the git server"] != "pending" {
		t.Fatalf("commands must wait for approval: %v", o)
	}
	for _, p := range []string{".hearth/ssh/id_rsa", ".ssh/id_rsa"} {
		fi, err := os.Stat(filepath.Join(home, p))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", p, err, fi)
		}
	}

	// Approve once; checks run and the manual step notices it is done.
	os.WriteFile(filepath.Join(home, ".registered"), nil, 0o600)
	opt.Approve = true
	o = ops(mustRun(t, rs, h, opt))
	if o["5:check config has gitsrv port"] != "ok" || o["6:manual register the public key in the git server"] != "ok" {
		t.Fatalf("after approval: %v", o)
	}
	// Third run without --yes: approvals persisted, nothing rewritten.
	opt.Approve = false
	for step, op := range ops(mustRun(t, rs, h, opt)) {
		if op != "ok" {
			t.Fatalf("not idempotent: %s %s", step, op)
		}
	}

	// A changed command needs approval again.
	rs[0].Steps[4].Run = "cat ~/.hearth/ssh/config; grep -c 'Port 8235' ~/.hearth/ssh/config"
	if o := ops(mustRun(t, rs, h, opt)); o["5:check config has gitsrv port"] != "needs-approval" {
		t.Fatalf("modified command ran with stale approval: %v", o)
	}

	// Policies: local edits are kept (if-absent) or reported (warn), never lost.
	os.WriteFile(filepath.Join(home, ".ssh/id_rsa"), []byte("NEWER LOCAL KEY"), 0o600)
	os.WriteFile(filepath.Join(home, ".hearth/ssh/config"), []byte("edited\n"), 0o600)
	o = ops(mustRun(t, rs, h, opt))
	if o["1:file ~/.hearth/ssh/id_rsa,~/.ssh/id_rsa"] != "ok" || o["2:file ~/.hearth/ssh/config"] != "warn" {
		t.Fatalf("policies: %v", o)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".ssh/id_rsa")); string(b) != "NEWER LOCAL KEY" {
		t.Fatal("if-absent overwrote a local file")
	}
}

func mustRun(t *testing.T, rs []*Recipe, h Host, opt Options) []Result {
	t.Helper()
	res, err := Run(rs, h, opt)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestEnvScriptIsDirectoryScoped(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	home, h, rs := setup(t)
	rc := filepath.Join(home, ".bashrc")
	os.WriteFile(rc, []byte("# someone else's settings\nexport OTHER=1\n"), 0o644)
	opt := Options{Mode: Apply, StateDir: filepath.Join(home, ".hearth"), RCFile: rc}
	mustRun(t, rs, h, opt)
	mustRun(t, rs, h, opt) // rerun must not duplicate the block
	b, _ := os.ReadFile(rc)
	if strings.Count(string(b), rcBegin(EnvScript(opt.StateDir))) != 1 || !strings.HasPrefix(string(b), "# someone else's settings\nexport OTHER=1\n") {
		t.Fatalf("rc file:\n%s", b)
	}
	os.MkdirAll(filepath.Join(home, "work/repo"), 0o755)
	probe := `. ` + rc + `; echo "out=[$GIT_SSH_COMMAND][$EDITOR]"; cd ` + filepath.Join(home, "work/repo") + `; _hearth_env; echo "in=[$GIT_SSH_COMMAND]"`
	cmd := exec.Command("bash", "-c", probe)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	want := "out=[][vim]\nin=[ssh -F " + home + "/.hearth/ssh/config]\n"
	if string(out) != want {
		t.Fatalf("got %q want %q", out, want)
	}
	if changed, _ := UninstallRC(rc, EnvScript(opt.StateDir)); !changed {
		t.Fatal("uninstall did nothing")
	}
	if b, _ := os.ReadFile(rc); string(b) != "# someone else's settings\nexport OTHER=1\n" {
		t.Fatalf("uninstall left: %q", b)
	}
}

func TestCaptureAndRunWithSecretStdin(t *testing.T) {
	home, h, _ := setup(t)
	r, err := Parse([]byte(`
name = "codex-mixin"
[[step]]
type = "capture"
run = "printf exported-config"
secret = "codex-mixin-config"
every = "6h"

[[step]]
type = "run"
when_secret = "codex-mixin-config"
unless = "test -f ~/imported"
run = "cat > ~/imported"
stdin_secret = "codex-mixin-config"

[[step]]
type = "manual"
unless_secret = "codex-mixin-config"
tty = true
text = "codex-mixin setup (scan the QR code)"
`))
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".hearth")
	rs := []*Recipe{r}
	// Before anything was captured: only the manual QR step is relevant.
	o := ops(mustRun(t, rs, h, Options{Mode: Apply, StateDir: state, Approve: true}))
	if o["2:run cat > ~/imported"] != "skipped" || o["3:manual codex-mixin setup (scan the QR code)"] != "pending" {
		t.Fatalf("before capture: %v", o)
	}
	if o := ops(mustRun(t, rs, h, Options{Mode: Save, StateDir: state, Approve: true})); o["1:capture printf exported-config"] != "applied" {
		t.Fatalf("capture: %v", o)
	}
	if o := ops(mustRun(t, rs, h, Options{Mode: Save, StateDir: state, OnlyDue: true})); o["1:capture printf exported-config"] != "skipped" {
		t.Fatalf("capture not throttled by every: %v", o)
	}
	// New machine with the captured secret: import instead of scanning.
	o = ops(mustRun(t, rs, h, Options{Mode: Apply, StateDir: state, Approve: true}))
	if o["2:run cat > ~/imported"] != "applied" || o["3:manual codex-mixin setup (scan the QR code)"] != "skipped" {
		t.Fatalf("after capture: %v", o)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "imported")); string(b) != "exported-config" {
		t.Fatalf("stdin secret: %q", b)
	}
	if o := ops(mustRun(t, rs, h, Options{Mode: Apply, StateDir: state})); o["2:run cat > ~/imported"] != "ok" {
		t.Fatalf("unless not honoured: %v", o)
	}
}

func TestRCBlocksOfTwoWorkspacesCoexist(t *testing.T) {
	rc := filepath.Join(t.TempDir(), ".bashrc")
	os.WriteFile(rc, []byte("export X=1\n"), 0o644)
	InstallRC(rc, "/ws/alice/.hearth/env/activate.sh")
	InstallRC(rc, "/ws/bob/.hearth/env/activate.sh")
	InstallRC(rc, "/ws/alice/.hearth/env/activate.sh") // rerun
	b, _ := os.ReadFile(rc)
	if strings.Count(string(b), ">>> hearth env") != 2 || !strings.Contains(string(b), "alice") || !strings.Contains(string(b), "bob") {
		t.Fatalf("rc:\n%s", b)
	}
	UninstallRC(rc, "/ws/bob/.hearth/env/activate.sh")
	b, _ = os.ReadFile(rc)
	if strings.Contains(string(b), "bob") || !strings.Contains(string(b), "alice") || !strings.HasPrefix(string(b), "export X=1\n") {
		t.Fatalf("after uninstall:\n%s", b)
	}
}

// macOS uses zsh: the rc file defaults to ~/.zshrc and the scoped env hooks
// into precmd instead of bash's PROMPT_COMMAND.
func TestEnvScriptZsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	home, h, rs := setup(t)
	t.Setenv("SHELL", "/bin/zsh")
	if got := DefaultRCFile(); got != filepath.Join(home, ".zshrc") {
		t.Fatalf("rc file %s", got)
	}
	opt := Options{Mode: Apply, StateDir: filepath.Join(home, ".hearth")}
	mustRun(t, rs, h, opt)
	rc := filepath.Join(home, ".zshrc")
	os.MkdirAll(filepath.Join(home, "work/repo"), 0o755)
	// Run the precmd hooks the way an interactive prompt would.
	probe := `. ` + rc + `; echo "out=[$GIT_SSH_COMMAND][$EDITOR]"; cd ` + filepath.Join(home, "work/repo") +
		`; for f in $precmd_functions; do $f; done; echo "in=[$GIT_SSH_COMMAND] hooks=$precmd_functions"`
	cmd := exec.Command(zsh, "-f", "-c", probe)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	want := "out=[][vim]\nin=[ssh -F " + home + "/.hearth/ssh/config] hooks=_hearth_env\n"
	if string(out) != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestWhenMachineAndRecipeLevel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	r, err := Parse([]byte(`name = "gpu"
when = { profile = "h800" }
[[step]]
type = "manual"
name = "nccl"
text = "set NCCL vars"
[[step]]
type = "manual"
name = "any-gpu"
text = "x"
when = { profile = "h800,gb200", gpu = "!none" }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]byte("name = \"x\"\n[[step]]\ntype = \"manual\"\ntext = \"x\"\nwhen = { gpus = \"h800\" }\n")); err == nil {
		t.Fatal("unknown when key accepted")
	}
	run := func(f machine.Facts, prof ...string) map[string]string {
		res := mustRun(t, []*Recipe{r}, &fakeHost{}, Options{Mode: Apply, StateDir: filepath.Join(home, ".as"), Machine: &f, Profiles: prof})
		return ops(res)
	}
	if o := run(machine.Facts{GPU: "h800"}, "h800"); o["1:manual nccl"] != "pending" || o["2:manual any-gpu"] != "pending" {
		t.Fatal(o)
	}
	if o := run(machine.Facts{GPU: "gb200"}, "gb200"); o["1:manual nccl"] != "skipped" || o["2:manual any-gpu"] != "pending" {
		t.Fatal(o) // step-level profile overrides the recipe's
	}
	if o := run(machine.Facts{OS: "darwin"}); o["1:manual nccl"] != "skipped" || o["2:manual any-gpu"] != "skipped" {
		t.Fatal(o)
	}
}

// ${WORKSPACE}/.ssh/config is the user's own file; without a workspace it
// would be the shared ~/.ssh/config, so only that step is skipped.
func TestWorkspaceTargetWithoutWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := "name='x'\n[[step]]\ntype='file'\nsecret='a'\nto=['${WORKSPACE}/.ssh/config']\n"
	t.Setenv("HEARTH_WORKSPACE", "")
	r, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("workspace target rejected: %v", err)
	}
	h := &fakeHost{secrets: map[string][]byte{"a": []byte("cfg")}}
	res, err := Run([]*Recipe{r}, h, Options{Mode: Apply, StateDir: filepath.Join(home, ".hearth")})
	if err != nil || len(res) != 1 || res[0].Op != "skipped" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh/config")); err == nil {
		t.Fatal("wrote the shared ~/.ssh/config")
	}
	ws := t.TempDir()
	t.Setenv("HEARTH_WORKSPACE", ws)
	res, _ = Run([]*Recipe{r}, h, Options{Mode: Apply, StateDir: filepath.Join(home, ".hearth")})
	if b, _ := os.ReadFile(filepath.Join(ws, ".ssh/config")); string(b) != "cfg" || res[0].Op != "applied" {
		t.Fatalf("workspace copy: %q %+v", b, res)
	}
}

// ${WORKSPACE} must mean the same thing in run/check commands as it does in
// file and env targets; the shell itself only knows HEARTH_WORKSPACE.
func TestWorkspaceExpandsInCommands(t *testing.T) {
	home, ws := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HEARTH_WORKSPACE", ws)
	r, err := Parse([]byte("name='x'\n[[step]]\ntype='check'\nname='ws'\nrun='test -d ${WORKSPACE}'\n"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run([]*Recipe{r}, &fakeHost{}, Options{Mode: Apply, Approve: true, StateDir: filepath.Join(home, ".hearth")})
	if err != nil || len(res) != 1 || res[0].Op != "ok" {
		t.Fatalf("%+v %v", res, err)
	}
}

// A manual step marked done stays done on this machine; a warning is not a
// failure.
func TestManualDoneAndWarnNotBad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	r, err := Parse([]byte("name = \"m\"\n[[step]]\ntype = \"manual\"\nname = \"qr\"\ntext = \"scan the QR code\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	st := filepath.Join(home, ".as")
	res := mustRun(t, []*Recipe{r}, &fakeHost{}, Options{Mode: Apply, StateDir: st, DryRun: true})
	if res[0].Op != "pending" || res[0].Key == "" {
		t.Fatalf("%+v", res)
	}
	res = mustRun(t, []*Recipe{r}, &fakeHost{}, Options{Mode: Apply, StateDir: st, DryRun: true, MarkDone: map[string]bool{res[0].Key: true}})
	if res[0].Op != "ok" || res[0].Detail != "marked done" {
		t.Fatalf("%+v", res)
	}
	res = mustRun(t, []*Recipe{r}, &fakeHost{}, Options{Mode: Apply, StateDir: st})
	if res[0].Op != "ok" {
		t.Fatalf("done step pending again: %+v", res)
	}
	if (Result{Op: "warn"}).Bad() || !(Result{Op: "failed"}).Bad() {
		t.Fatal("warn counted as failure")
	}
}
