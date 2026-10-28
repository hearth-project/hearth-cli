package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/mcpserver"
)

// user registers another account and returns a logged-in machine for it.
func (en *env) user(email, device string) *machine {
	en.t.Helper()
	inv, _ := en.srv.CreateInvite(time.Hour)
	if _, err := Register(en.t.TempDir(), en.url, email, []byte(pw), inv); err != nil {
		en.t.Fatal(err)
	}
	home := en.t.TempDir()
	if _, err := Login(home, en.url, email, []byte(pw), device); err != nil {
		en.t.Fatal(err)
	}
	e, err := NewEngine(home)
	if err != nil {
		en.t.Fatal(err)
	}
	codex := en.t.TempDir()
	e.Adapters = []adapter.Adapter{adapter.CodexAt(codex), adapter.ClaudeAt(en.t.TempDir())}
	e.Logf = en.t.Logf
	return &machine{Engine: e, codex: codex}
}

func TestTeamPublishLinkUpdate(t *testing.T) {
	en := newEnv(t)
	alice := en.machine("alice-laptop") // dev@example.com
	bob := en.user("bob@example.com", "bob-box")
	eve := en.user("eve@example.com", "eve-box")
	bobCodex, bobClaude := bob.libTargets(t)

	if err := alice.API.CreateTeam("sglang-team"); err != nil {
		t.Fatal(err)
	}
	if err := alice.API.SetMember("sglang-team", "bob@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	if err := alice.API.SetMember("sglang-team", "nobody@example.com", "member"); StatusOf(err) != 400 {
		t.Fatalf("unknown user: %v", err)
	}
	if err := alice.API.RemoveMember("sglang-team", "dev@example.com"); StatusOf(err) != 400 {
		t.Fatalf("removing the last owner must fail: %v", err)
	}

	libWrite(t, alice, "skills/bench/SKILL.md", "---\nname: bench\ndescription: shared benchmark skill\n---\nv1 steps\n")
	libWrite(t, alice, "agents/reviewer.md", "---\nname: reviewer\ndescription: reviews\n---\nReview v1.\n")
	if v, created, err := alice.Publish("sglang-team", "skill", "bench", false); err != nil || v != 1 || !created {
		t.Fatalf("publish: %d %v %v", v, created, err)
	}
	if v, created, _ := alice.Publish("sglang-team", "skill", "bench", false); v != 1 || created {
		t.Fatal("republishing identical content created a version")
	}
	if _, _, err := alice.Publish("sglang-team", "agent", "reviewer", false); err != nil {
		t.Fatal(err)
	}

	// Permissions: members cannot publish, outsiders cannot see the team.
	libWrite(t, bob, "skills/mine/SKILL.md", "---\nname: mine\n---\nx\n")
	if _, _, err := bob.Publish("sglang-team", "skill", "mine", false); StatusOf(err) != 403 {
		t.Fatalf("member publish: %v", err)
	}
	if _, err := eve.API.Packages("sglang-team"); StatusOf(err) != 404 {
		t.Fatalf("outsider list: %v", err)
	}

	// Bob subscribes and links; both agents get the skill and the agent.
	if err := AddSource(bob.Home, Source{Name: "team", Type: "team", Team: "sglang-team"}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []struct{ ref, typ string }{{"bench", "skill"}, {"team:reviewer", "agent"}} {
		c, err := bob.ResolvePackage(ref.ref, ref.typ)
		if err != nil {
			t.Fatal(err)
		}
		_, sum, err := bob.Fetch(c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bob.AddLink(c, sum); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := bob.Render(false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(bobCodex, "skills/bench/SKILL.md")); !strings.Contains(string(b), "v1 steps") {
		t.Fatal("linked skill not rendered")
	}
	if b, _ := os.ReadFile(filepath.Join(bobClaude, "agents/reviewer.md")); !strings.Contains(string(b), "Review v1.") {
		t.Fatal("linked agent not rendered")
	}
	if _, err := bob.ResolvePackage("mine", "skill"); err == nil || !strings.Contains(err.Error(), "local library") {
		t.Fatalf("local item must win: %v", err)
	}

	// A new version shows up as an update with a file diff; pins hold until applied.
	libWrite(t, alice, "skills/bench/SKILL.md", "---\nname: bench\ndescription: shared benchmark skill\n---\nv2 steps\n")
	libWrite(t, alice, "skills/bench/run.sh", "echo\n")
	alice.Publish("sglang-team", "skill", "bench", false)
	ups, err := bob.CheckUpdates("")
	if err != nil || len(ups) != 1 || ups[0].New.Version != 2 {
		t.Fatalf("updates: %+v %v", ups, err)
	}
	if got := strings.Join(ups[0].Changes, ","); got != "~ SKILL.md,+ run.sh" {
		t.Fatalf("diff: %s", got)
	}
	bob.Render(false)
	if b, _ := os.ReadFile(filepath.Join(bobCodex, "skills/bench/SKILL.md")); !strings.Contains(string(b), "v1 steps") {
		t.Fatal("pinned version changed without update")
	}
	bob.AddLink(ups[0].New, ups[0].NewSum)
	bob.Render(false)
	if b, _ := os.ReadFile(filepath.Join(bobCodex, "skills/bench/SKILL.md")); !strings.Contains(string(b), "v2 steps") {
		t.Fatal("update not applied")
	}

	// MCP search sees linked skills and trusted, not-yet-linked ones.
	libWrite(t, alice, "skills/profiler/SKILL.md", "---\nname: profiler\ndescription: nsys torch profiler traces\n---\nprofile steps\n")
	alice.Publish("sglang-team", "skill", "profiler", false)
	svc := &mcpserver.Service{Remote: bob.SearchableSkills, Fetch: bob.FetchSkill}
	ix, err := svc.Index()
	if err != nil {
		t.Fatal(err)
	}
	if sk, ok := ix.Get("bench"); !ok || sk.Dir == "" || sk.Source != "linked" {
		t.Fatalf("linked skill in search: %+v", sk)
	}
	res := bob.SearchTeams("nsys profiler trace", 3, 5*time.Second)
	if len(res) == 0 || res[0].Name != "profiler" || res[0].Dir != "" || !strings.Contains(res[0].Source, "not linked") {
		t.Fatalf("server-side team search: %+v", res)
	}
	if d, err := bob.FetchSkill(res[0].Skill); err != nil || !exists(filepath.Join(d, "SKILL.md")) {
		t.Fatalf("fetch remote skill: %v", err)
	}
	// Body text is indexed on the server, not only names/descriptions.
	if res := bob.SearchTeams("steps", 3, 5*time.Second); len(res) == 0 {
		t.Fatal("skill body not indexed on the server")
	}

	// Links travel with the library: Bob's second machine materializes them.
	if _, err := bob.LibPush(false); err != nil {
		t.Fatal(err)
	}
	home2 := t.TempDir()
	if _, err := Login(home2, en.url, "bob@example.com", []byte(pw), "bob-2"); err != nil {
		t.Fatal(err)
	}
	b2, _ := NewEngine(home2)
	b2codex := filepath.Join(home2, "t-codex")
	m2 := &machine{Engine: b2}
	m2.libTargets(t)
	if _, err := b2.LibPull(false); err != nil {
		t.Fatal(err)
	}
	acts, err := b2.Render(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		if a.Op == "warn" {
			t.Logf("warn: %s %s", a.Item, a.Detail)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(b2codex, "skills/bench/SKILL.md")); !strings.Contains(string(b), "v2 steps") {
		t.Fatal("second machine did not materialize the linked skill")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestGitSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	en := newEnv(t)
	a := en.machine("a")
	codex, _ := a.libTargets(t)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	os.MkdirAll(filepath.Join(repo, "pack/skills/pdf"), 0o755)
	os.WriteFile(filepath.Join(repo, "pack/skills/pdf/SKILL.md"), []byte("---\ndescription: read pdf files\n---\nuse pdftotext\n"), 0o644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "one")

	if err := AddSource(a.Home, Source{Name: "community", Type: "git", URL: "file://" + repo, Subdir: "pack"}); err != nil {
		t.Fatal(err)
	}
	if ss, _ := LoadSources(a.Home); ss[0].Trust != "untrusted" {
		t.Fatal("git sources must default to untrusted")
	}
	if err := AddSource(a.Home, Source{Name: "bad", Type: "git", URL: "--upload-pack=evil"}); err == nil {
		t.Fatal("option-like git URL accepted")
	}
	c, err := a.ResolvePackage("community:pdf", "skill")
	if err != nil || c.Description != "read pdf files" || len(c.Commit) != 40 {
		t.Fatalf("resolve: %+v %v", c, err)
	}
	_, sum, err := a.Fetch(c)
	if err != nil {
		t.Fatal(err)
	}
	a.AddLink(c, sum)
	a.Render(false)
	if b, _ := os.ReadFile(filepath.Join(codex, "skills/pdf/SKILL.md")); !strings.Contains(string(b), "pdftotext") {
		t.Fatal("git skill not rendered")
	}

	os.WriteFile(filepath.Join(repo, "pack/skills/pdf/SKILL.md"), []byte("---\ndescription: read pdf files\n---\nuse poppler\n"), 0o644)
	git(t, repo, "commit", "-q", "-am", "two")
	ups, err := a.CheckUpdates("pdf")
	if err != nil || len(ups) != 1 || ups[0].New.Commit == c.Commit || ups[0].Changes[0] != "~ SKILL.md" {
		t.Fatalf("git update: %+v %v", ups, err)
	}

	// Losing the cache (new machine) re-fetches the pinned commit, not the latest.
	os.RemoveAll(filepath.Join(a.Home, "cache"))
	a.Render(false)
	if b, _ := os.ReadFile(filepath.Join(codex, "skills/pdf/SKILL.md")); !strings.Contains(string(b), "pdftotext") {
		t.Fatalf("pinned commit not restored: %s", b)
	}
}

// Invite codes add members; a team can let another team's members read
// (not publish) its packages; joined and readable teams become sources.
func TestTeamInviteJoinAndShareWithTeam(t *testing.T) {
	en := newEnv(t)
	alice := en.machine("alice") // dev@example.com
	bob := en.user("bob@example.com", "bob")
	carol := en.user("carol@example.com", "carol")
	eve := en.user("eve@example.com", "eve")

	if err := alice.API.CreateTeam("infer"); err != nil {
		t.Fatal(err)
	}
	libWrite(t, alice, "skills/bench/SKILL.md", "---\nname: bench\ndescription: shared benchmark skill\n---\nsteps\n")
	if _, _, err := alice.Publish("infer", "skill", "bench", false); err != nil {
		t.Fatal(err)
	}
	inv, err := alice.API.CreateTeamInvite("infer", api.CreateInviteRequest{MaxUses: 1})
	if err != nil || inv.Role != "maintainer" || !strings.HasPrefix(inv.Code, "team-") {
		t.Fatalf("invite %+v %v", inv, err)
	}
	if tm, err := bob.API.JoinTeam(inv.Code); err != nil || tm.Name != "infer" || tm.Role != "maintainer" {
		t.Fatalf("join %+v %v", tm, err)
	}
	if _, err := eve.API.JoinTeam(inv.Code); StatusOf(err) != 400 {
		t.Fatalf("used-up code accepted: %v", err)
	}
	if _, err := eve.API.JoinTeam("team-nope"); StatusOf(err) != 400 {
		t.Fatalf("unknown code: %v", err)
	}
	// Maintainers can share; members of the team see it as a source.
	libWrite(t, bob, "skills/prof/SKILL.md", "---\nname: prof\ndescription: profiling\n---\nx\n")
	if _, _, err := bob.Publish("infer", "skill", "prof", false); err != nil {
		t.Fatal(err)
	}
	teams, changed, err := bob.SyncTeamSources()
	if err != nil || len(teams) != 1 || !changed {
		t.Fatalf("sync %+v %v %v", teams, changed, err)
	}
	if ss, _ := LoadSources(bob.Home); len(ss) != 1 || ss[0].Team != "infer" || !ss[0].Auto || ss[0].Trust != "trusted" {
		t.Fatalf("sources %+v", ss)
	}

	// Team "train" (carol) reads infer's packages once infer shares with it.
	if err := carol.API.CreateTeam("train"); err != nil {
		t.Fatal(err)
	}
	if _, err := carol.API.Packages("infer"); StatusOf(err) != 404 {
		t.Fatalf("before sharing: %v", err)
	}
	if err := bob.API.ShareTeam("infer", "train", true); StatusOf(err) != 403 {
		t.Fatalf("non-owner shared: %v", err)
	}
	if err := alice.API.ShareTeam("infer", "train", true); err != nil {
		t.Fatal(err)
	}
	ps, err := carol.API.Packages("infer")
	if err != nil || len(ps) != 2 {
		t.Fatalf("reader list %+v %v", ps, err)
	}
	if _, _, _, err := carol.API.GetPackage("infer", "skill", "bench", 0); err != nil {
		t.Fatal(err)
	}
	if hits, err := carol.API.SearchTeam("infer", "benchmark", 5); err != nil || len(hits) == 0 {
		t.Fatalf("reader search %+v %v", hits, err)
	}
	libWrite(t, carol, "skills/x/SKILL.md", "---\nname: x\n---\nx\n")
	if _, _, err := carol.Publish("infer", "skill", "x", false); StatusOf(err) != 404 && StatusOf(err) != 403 {
		t.Fatalf("reader published: %v", err)
	}
	if _, err := carol.API.Members("infer"); err == nil {
		t.Fatal("reader listed members")
	}
	teams, _, _ = carol.SyncTeamSources()
	roles := map[string]string{}
	for _, tm := range teams {
		roles[tm.Name] = tm.Role + "/" + tm.Via
	}
	if roles["train"] != "owner/" || roles["infer"] != "reader/train" {
		t.Fatalf("carol's teams %+v", teams)
	}
	if sh, err := alice.API.TeamShares("infer"); err != nil || len(sh.SharedWith) != 1 || sh.SharedWith[0] != "train" {
		t.Fatalf("shares %+v %v", sh, err)
	}
	// Stop sharing: carol loses access and the automatic source.
	if err := alice.API.ShareTeam("infer", "train", false); err != nil {
		t.Fatal(err)
	}
	if _, err := carol.API.Packages("infer"); StatusOf(err) != 404 {
		t.Fatalf("after unsharing: %v", err)
	}
	carol.SyncTeamSources()
	for _, s := range must(LoadSources(carol.Home)) {
		if s.Team == "infer" {
			t.Fatal("source of an unshared team kept")
		}
	}
	// Leaving drops the source too.
	if err := bob.API.RemoveMember("infer", "bob@example.com"); err != nil {
		t.Fatal(err)
	}
	bob.SyncTeamSources()
	if ss, _ := LoadSources(bob.Home); len(ss) != 0 {
		t.Fatalf("source kept after leaving: %+v", ss)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
