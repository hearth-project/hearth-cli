package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mach "github.com/hearth-project/hearth-cli/internal/machine"
	"github.com/hearth-project/hearth-cli/internal/paths"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// One library, several kinds of machines: habits apply everywhere, other
// steps only on their profile, and each profile has its own workspace.
func TestMachineProfilesAcrossMachines(t *testing.T) {
	en := newEnv(t)
	a := en.machine("h800-box")
	home, ws := learnEnv(t)
	t.Setenv("HEARTH_MACHINE", `{"os":"linux","arch":"x86_64","gpu":"h800"}`)
	t.Setenv("CUDA_HOME", "/usr/local/cuda-12.4")
	t.Setenv("GIT_SSH_COMMAND", "ssh -F ${WORKSPACE}/.ssh/config")
	os.MkdirAll(filepath.Join(home, ".config/gh"), 0o700)
	os.WriteFile(filepath.Join(home, ".config/gh/hosts.yml"), []byte("token: h800\n"), 0o600)

	fs := findingIDs(a.Discover())
	if !fs["env:CUDA_HOME"].PerMachine || fs["env:GIT_SSH_COMMAND"].PerMachine {
		t.Fatalf("per-machine hints: cuda=%v git=%v", fs["env:CUDA_HOME"].PerMachine, fs["env:GIT_SSH_COMMAND"].PerMachine)
	}
	prof, created, err := EnsureHereProfile(a.Home)
	if err != nil || prof != "h800" || !created {
		t.Fatalf("profile %q %v %v", prof, created, err)
	}
	m, _ := Machine(a.Home)
	if p, _ := m.File.Get("h800"); p.Workspace != ws {
		t.Fatalf("profile workspace %q, want %q", p.Workspace, ws)
	}
	learned, _ := LoadLearned(a.Home)
	if _, err := a.Accept(fs["env:GIT_SSH_COMMAND"], learned); err != nil { // habit
		t.Fatal(err)
	}
	if _, err := a.AcceptFor(fs["env:CUDA_HOME"], learned, prof); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AcceptFor(fs["gh-hosts"], learned, prof); err != nil { // per-machine copy of a file
		t.Fatal(err)
	}
	// A Mac profile with its own workspace and step, defined from the H800 box.
	m.File.Set(mach.Profile{Name: "mac", Match: map[string]string{"os": "darwin"}, Workspace: "${HOME}/work"})
	if err := m.File.Save(MachinesPath(a.Home)); err != nil {
		t.Fatal(err)
	}
	s, _ := UserStep("brew install ripgrep", 1)
	MergeSteps(learned, ScopeSteps([]recipe.Step{s}, "mac"))
	if err := SaveRecipe(a.Home, learned); err != nil {
		t.Fatal(err)
	}
	if st := a.LearnStatus([]Finding{fs["env:CUDA_HOME"]}, learned); st["env:CUDA_HOME"] != "known" {
		t.Fatalf("scoped finding not known on its machine: %v", st)
	}
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}

	run := func(e *machine, facts string) map[string]recipe.Result {
		t.Helper()
		t.Setenv("HEARTH_MACHINE", facts)
		t.Setenv("HEARTH_WORKSPACE", "")
		t.Setenv("HEARTH_WORKSPACE_FROM_PROFILE", "")
		if _, err := e.LibPull(false); err != nil {
			t.Fatal(err)
		}
		if _, err := EnsureProfileWorkspace(e.Home); err != nil {
			t.Fatal(err)
		}
		res, err := e.RunRecipes(nil, recipe.Options{Mode: recipe.Apply, RCFile: filepath.Join(t.TempDir(), "rc")})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]recipe.Result{}
		for _, r := range res {
			f := strings.Fields(r.Step)
			out[f[len(f)-1]] = r
		}
		return out
	}

	// Another H800 machine: habit + H800 steps, same workspace path.
	b := en.machine("h800-2")
	home2, _ := learnEnv(t)
	r := run(b, `{"os":"linux","arch":"x86_64","gpu":"h800"}`)
	envOf := func(e *machine) string { b, _ := os.ReadFile(recipe.EnvScript(e.Home)); return string(b) }
	if env := envOf(b); !strings.Contains(env, "CUDA_HOME='/usr/local/cuda-12.4'") || !strings.Contains(env, "GIT_SSH_COMMAND") || r["user-1@mac"].Op != "skipped" {
		t.Fatalf("h800: %+v\n%s", r, env)
	}
	if got, _ := os.ReadFile(filepath.Join(home2, ".config/gh/hosts.yml")); string(got) != "token: h800\n" {
		t.Fatalf("h800 gh login %q", got)
	}
	if paths.Workspace() != ws {
		t.Fatalf("h800 workspace %q want %q", paths.Workspace(), ws)
	}

	// A Mac: habit + Mac step, no CUDA, no H800 credentials, its own workspace.
	c := en.machine("mac")
	home3, _ := learnEnv(t)
	r = run(c, `{"os":"darwin","arch":"arm64","gpu":"none"}`)
	if env := envOf(c); !strings.Contains(env, "GIT_SSH_COMMAND") || strings.Contains(env, "CUDA_HOME") ||
		r["env:CUDA_HOME@h800"].Op != "skipped" || r["user-1@mac"].Op != "pending" {
		t.Fatalf("mac: %+v\n%s", r, env)
	}
	if !strings.Contains(r["env:CUDA_HOME@h800"].Detail, "profile h800") {
		t.Fatalf("skip reason %q", r["env:CUDA_HOME@h800"].Detail)
	}
	if _, err := os.Stat(filepath.Join(home3, ".config/gh/hosts.yml")); err == nil {
		t.Fatal("H800 credential copied to the Mac")
	}
	if want := filepath.Join(home3, "work"); paths.Workspace() != want {
		t.Fatalf("mac workspace %q want %q", paths.Workspace(), want)
	} else if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatal("mac workspace not created")
	}
	// An explicit workspace (hearth init) wins over the profile.
	t.Setenv("HEARTH_WORKSPACE", "/explicit")
	t.Setenv("HEARTH_WORKSPACE_FROM_PROFILE", "")
	if got, _ := EnsureProfileWorkspace(c.Home); got != "" || paths.Workspace() != "/explicit" {
		t.Fatalf("explicit workspace overridden: %q", paths.Workspace())
	}
}
