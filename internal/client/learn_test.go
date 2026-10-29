package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// learnEnv points HOME, the workspace and PATH at temp dirs so discovery
// only sees what the test creates.
func learnEnv(t *testing.T) (home, ws string) {
	home, ws = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HEARTH_WORKSPACE", ws)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, v := range []string{"GIT_SSH_COMMAND", "VIMINIT", "npm_config_registry", "NPM_CONFIG_REGISTRY", "HTTPS_PROXY", "https_proxy", "NO_PROXY"} {
		t.Setenv(v, "")
	}
	return
}

func findingIDs(fs []Finding) map[string]Finding {
	m := map[string]Finding{}
	for _, f := range fs {
		m[f.ID] = f
	}
	return m
}

func TestLearnAcceptRestoresOnNewMachine(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	home, ws := learnEnv(t)
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n"
	os.MkdirAll(filepath.Join(ws, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(ws, ".ssh/id_ed25519"), []byte(key), 0o600)
	os.WriteFile(filepath.Join(ws, ".ssh/id_ed25519.pub"), []byte("ssh-ed25519 AAA"), 0o644)
	os.WriteFile(filepath.Join(ws, ".ssh/config"),
		[]byte("Host gitsrv\n  User me\n  IdentityFile "+filepath.Join(ws, ".ssh/id_ed25519")+"\n"), 0o600)
	os.MkdirAll(filepath.Join(home, ".config/gh"), 0o700)
	os.WriteFile(filepath.Join(home, ".config/gh/hosts.yml"), []byte("github.com:\n  oauth_token: gho_x\n"), 0o600)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(ws, ".gitconfig"))

	fs := findingIDs(a.Discover())
	want := []string{"ssh-identity:${WORKSPACE}/.ssh/config", "gh-hosts", "env:GIT_CONFIG_GLOBAL"}
	for _, id := range want {
		if _, ok := fs[id]; !ok {
			t.Fatalf("missing finding %s in %v", id, fs)
		}
	}
	if _, ok := fs["ssh-key:${WORKSPACE}/.ssh/id_ed25519"]; ok {
		t.Fatal("key referenced by the ssh identity offered twice")
	}
	if _, ok := fs["env:GIT_SSH_COMMAND"]; ok {
		t.Fatal("GIT_SSH_COMMAND offered separately from its ssh identity")
	}
	ident := fs["ssh-identity:${WORKSPACE}/.ssh/config"]
	var keyStep, envStep, checkStep bool
	for _, s := range ident.steps {
		switch {
		case s.Type == "file" && s.Name == "ssh-key:${WORKSPACE}/.ssh/id_ed25519" && s.Secret == "file-ssh-id_ed25519":
			keyStep = true
		case s.Type == "env" && s.Set["GIT_SSH_COMMAND"] == "ssh -F ${WORKSPACE}/.ssh/config":
			envStep = s.Alias["sshx"] == "ssh -F ${WORKSPACE}/.ssh/config" &&
				len(s.Scope) == 1 && s.Scope[0] == "${WORKSPACE}"
		case s.Type == "check" && s.Run == "ssh -F ${WORKSPACE}/.ssh/config -G gitsrv":
			checkStep = true
		}
	}
	if !keyStep || !envStep || !checkStep {
		t.Fatalf("ssh identity is not wired end to end: %+v", ident.steps)
	}
	if _, ok := fs["ssh-key:${WORKSPACE}/.ssh/id_ed25519.pub"]; ok {
		t.Fatal("public key reported as a secret")
	}
	if got := fs["env:GIT_CONFIG_GLOBAL"].Detail; got != "${WORKSPACE}/.gitconfig" {
		t.Fatalf("env value not made portable: %q", got)
	}

	learned, err := LoadLearned(a.Home)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range want[:2] {
		if _, err := a.Accept(fs[id], learned); err != nil {
			t.Fatal(err)
		}
	}
	if err := IgnoreFinding(a.Home, "env:GIT_CONFIG_GLOBAL"); err != nil {
		t.Fatal(err)
	}
	if s, ok := UserStep("$ echo /tmp/x", 1); !ok || s.Type != "run" {
		t.Fatalf("user step %+v", s)
	} else {
		MergeSteps(learned, []recipe.Step{s})
	}
	if s, ok := UserStep("log in to the VPN", 2); !ok || s.Type != "manual" {
		t.Fatalf("user step %+v", s)
	} else {
		MergeSteps(learned, []recipe.Step{s})
	}
	if err := SaveRecipe(a.Home, learned); err != nil {
		t.Fatal(err)
	}
	// Round trip: what we marshal must parse back strictly, and accepting
	// the same finding again must not duplicate steps.
	again, err := LoadLearned(a.Home)
	if err != nil {
		t.Fatalf("saved recipe does not parse: %v", err)
	}
	n := len(again.Steps)
	if n != 7 {
		t.Fatalf("want 7 steps, got %d:\n%s", n, StepTOML(again.Steps))
	}
	if _, err := a.Accept(findingIDs(a.Discover())["gh-hosts"], again); err != nil || len(again.Steps) != n {
		t.Fatalf("re-accept duplicated steps (%d -> %d) %v", n, len(again.Steps), err)
	}
	cur := a.Discover()
	st := a.LearnStatus(cur, again)
	if st["gh-hosts"] != "known" || st["ssh-identity:${WORKSPACE}/.ssh/config"] != "known" {
		t.Fatalf("status before change: %v", st)
	}
	os.WriteFile(filepath.Join(home, ".config/gh/hosts.yml"), []byte("github.com:\n  oauth_token: gho_new\n"), 0o600)
	if st = a.LearnStatus(cur, again); st["gh-hosts"] != "changed" {
		t.Fatalf("status after change: %v", st)
	}
	if _, err := a.Accept(findingIDs(cur)["gh-hosts"], again); err != nil {
		t.Fatal(err)
	}
	if st = a.LearnStatus(cur, again); st["gh-hosts"] != "known" {
		t.Fatalf("status after update: %v", st)
	}
	if Ignored(a.Home)["env:GIT_CONFIG_GLOBAL"] != true {
		t.Fatal("ignore not recorded")
	}
	if _, err := a.LibPush(false); err != nil {
		t.Fatalf("learned library must pass the secret scan: %v", err)
	}

	// New machine with a different home and workspace.
	b := en.machine("b")
	home2, ws2 := learnEnv(t)
	if _, err := b.LibPull(false); err != nil {
		t.Fatal(err)
	}
	if !Ignored(b.Home)["env:GIT_CONFIG_GLOBAL"] {
		t.Fatal("never-ask list did not sync")
	}
	res, err := b.RunRecipes([]string{LearnedRecipe}, recipe.Options{Mode: recipe.Apply})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(ws2, ".ssh/id_ed25519")); string(got) != key {
		t.Fatalf("ssh key not restored: %q %+v", got, res)
	}
	if got, _ := os.ReadFile(filepath.Join(ws2, ".ssh/config")); !strings.Contains(string(got), "Host gitsrv") {
		t.Fatalf("ssh config not restored: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(home2, ".config/gh/hosts.yml")); !strings.Contains(string(got), "gho_new") {
		t.Fatalf("gh login not restored: %q", got)
	}
	ops := map[string]string{}
	for _, r := range res {
		f := strings.Fields(r.Step)
		ops[f[len(f)-1]] = r.Op
	}
	if ops["user-1"] != "needs-approval" || ops["user-2"] != "pending" {
		t.Fatalf("user steps: %v", ops)
	}
}

func TestProposals(t *testing.T) {
	home := t.TempDir()
	if _, err := Propose(home, "", "[[step]]\ntype = \"bogus\"\n", "x"); err == nil {
		t.Fatal("invalid step type accepted")
	}
	if _, err := Propose(home, "", "[[step]]\ntype = \"run\"\nrun = \"true\"\nwat = 1\n", "x"); err == nil {
		t.Fatal("unknown field accepted")
	}
	id1, err := Propose(home, "", "[[step]]\ntype = \"run\"\nname = \"nvm\"\nrun = \"echo hi\"\n", "installed nvm with the user")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := Propose(home, "extra", "[[step]]\ntype = \"manual\"\ntext = \"scan the QR code\"\n", "login")
	if err != nil {
		t.Fatal(err)
	}
	ps, _ := Proposals(home)
	if len(ps) != 2 {
		t.Fatalf("proposals: %+v", ps)
	}
	if rs, _ := recipe.LoadDir(RecipesDir(home)); len(rs) != 0 {
		t.Fatalf("pending proposals must not be live recipes: %+v", rs)
	}
	if _, err := ResolveProposal(home, id1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveProposal(home, id2, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveProposal(home, id2, true); err == nil {
		t.Fatal("resolved twice")
	}
	rs, err := recipe.LoadDir(RecipesDir(home))
	if err != nil || len(rs) != 1 || rs[0].Name != LearnedRecipe || rs[0].Steps[0].Name != "nvm" {
		t.Fatalf("recipes after accept: %+v %v", rs, err)
	}
	if ps, _ := Proposals(home); len(ps) != 0 {
		t.Fatalf("left: %+v", ps)
	}
	if !strings.Contains(RecipesSummary(home), "echo hi") {
		t.Fatal(RecipesSummary(home))
	}
}

// Token files listed in library/learn.json are offered as credentials.
func TestLearnTokenFilesFromSettings(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	home, _ := learnEnv(t)
	os.MkdirAll(filepath.Join(home, ".config/sso"), 0o700)
	os.WriteFile(filepath.Join(home, ".config/sso/token_me"), []byte("tok"), 0o600)
	if _, ok := findingIDs(a.Discover())["token:~/.config/sso/token_me"]; ok {
		t.Fatal("offered before it was configured")
	}
	os.MkdirAll(LibraryDir(a.Home), 0o755)
	os.WriteFile(filepath.Join(LibraryDir(a.Home), "learn.json"), []byte(`{"token_files":["~/.config/sso/token_*"]}`), 0o644)
	if _, ok := findingIDs(a.Discover())["token:~/.config/sso/token_me"]; !ok {
		t.Fatal("configured token file not offered")
	}
}

func TestUpgradeLearnedCodexMixin(t *testing.T) {
	r := &recipe.Recipe{Name: LearnedRecipe, Steps: []recipe.Step{
		{Type: "capture", Name: "codex-mixin-export", Run: "codex-mixin config export", Secret: "codex-mixin-config"},
		{Type: "manual", Name: "user-1", Text: "x"}}}
	if !upgradeLearned(r) {
		t.Fatal("not upgraded")
	}
	names := map[string]recipe.Step{}
	for _, s := range r.Steps {
		names[s.Name] = s
	}
	if !strings.Contains(names["codex-mixin-export"].Run, "mktemp") || names["codex-mixin-install"].DoneWhen == "" || names["user-1"].Text != "x" {
		t.Fatalf("%+v", r.Steps)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexMixinStepsParse(t *testing.T) {
	for _, s := range codexMixinSteps() {
		for _, c := range []string{s.Run, s.Unless, s.DoneWhen} {
			if c == "" {
				continue
			}
			if out, err := exec.Command("sh", "-n", "-c", c).CombinedOutput(); err != nil {
				t.Fatalf("%s: %v %s", s.Name, err, out)
			}
		}
	}
}

// On an account several people share, only this user's Comate login is
// offered; the others' login files are theirs.
func TestLearnOnlyOwnComateLogin(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	home, _ := learnEnv(t)
	dir := filepath.Join(home, ".comate/login-user")
	os.MkdirAll(dir, 0o700)
	for _, n := range []string{"dev", "colleague"} {
		os.WriteFile(filepath.Join(dir, n), []byte("Bearer-x"), 0o600)
	}
	ids := findingIDs(a.Discover())
	if _, ok := ids["token:~/.comate/login-user/dev"]; !ok {
		t.Fatal("own Comate login not offered")
	}
	if _, ok := ids["token:~/.comate/login-user/colleague"]; ok {
		t.Fatal("another person's Comate login offered")
	}
}

func TestDropOthersComateLogins(t *testing.T) {
	r := &recipe.Recipe{Name: LearnedRecipe, Steps: []recipe.Step{
		{Type: "file", Name: "token:~/.comate/login-user/dev"},
		{Type: "file", Name: "token:~/.comate/login-user/colleague"},
		{Type: "file", Name: "gh-hosts"}}}
	if !dropOthersComateLogins(r, "dev@example.com") || len(r.Steps) != 2 || r.Steps[1].Name != "gh-hosts" {
		t.Fatalf("%+v", r.Steps)
	}
	if dropOthersComateLogins(r, "dev@example.com") {
		t.Fatal("second run changed it")
	}
}
