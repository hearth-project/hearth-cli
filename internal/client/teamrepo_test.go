package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/api"
)

// A team bound to a repository: the owner's machine publishes the
// repository's skills (nested in categories) and agents into the team,
// proposes the team's own items back as a review once, picks up the
// repository's later changes, and other people cannot bind or report.
func TestTeamRepoSync(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	en := newEnv(t)
	alice := en.machine("alice-box")
	bob := en.user("bob@example.com", "bob-box")
	if err := alice.API.CreateTeam("infer"); err != nil {
		t.Fatal(err)
	}
	if err := alice.API.SetMember("infer", "bob@example.com", "maintainer"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_AUTHOR_NAME", "alice")
	t.Setenv("GIT_AUTHOR_EMAIL", "dev@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "alice")
	t.Setenv("GIT_COMMITTER_EMAIL", "dev@example.com")

	// The upstream repository (bare) with skills/<category>/<skill> and agents.
	bare := t.TempDir()
	git(t, bare, "init", "-q", "--bare", "-b", "master")
	work := t.TempDir()
	git(t, work, "init", "-q", "-b", "master")
	write := func(rel, s string) {
		p := filepath.Join(work, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	write("skills/connectors/kudoc/SKILL.md", "---\nname: kudoc\ndescription: knowledge base\n---\nv1\n")
	write("skills/connectors/kudoc/bin/run.sh", "echo hi\n")
	write("skills/connectors/kusheet/SKILL.md", "---\nname: kusheet\ndescription: sheets\n---\nold in repo\n")
	write("agents/verifier.md", "---\nname: verifier\ndescription: verifies\ntools: Bash\n---\nVerify.\n")
	write("agents/verifier.toml", "name = \"verifier\"\n")
	write("docs/readme.md", "not a skill\n")
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "init")
	git(t, work, "remote", "add", "origin", bare)
	git(t, work, "push", "-q", "origin", "master")

	// Only the owner binds, and only to one of their own machines.
	if _, err := bob.API.SetTeamRepo("infer", apiRepo(bare, bob.Cfg.DeviceID)); StatusOf(err) != 403 {
		t.Fatalf("maintainer bound: %v", err)
	}
	if _, err := alice.API.SetTeamRepo("infer", apiRepo(bare, bob.Cfg.DeviceID)); StatusOf(err) != 400 {
		t.Fatalf("someone else's machine: %v", err)
	}
	if _, err := alice.API.SetTeamRepo("infer", apiRepo("x; rm -rf /", alice.Cfg.DeviceID)); StatusOf(err) != 400 {
		t.Fatalf("bad url: %v", err)
	}
	if _, err := alice.API.SetTeamRepo("infer", apiRepo(bare, alice.Cfg.DeviceID)); StatusOf(err) != 400 {
		t.Fatalf("a local path is not a repository URL: %v", err)
	}
	// Tests use a file:// URL through ssh:// validation: point it at the bare repo.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+bare+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "ssh://git.example.com/infer")
	if _, err := alice.API.SetTeamRepo("infer", apiRepo("ssh://git.example.com/infer", alice.Cfg.DeviceID)); err != nil {
		t.Fatal(err)
	}
	jobs, err := alice.API.MyTeamRepos()
	if err != nil || len(jobs) != 1 || !jobs[0].Due {
		t.Fatalf("jobs %+v %v", jobs, err)
	}
	if js, _ := bob.API.MyTeamRepos(); len(js) != 0 {
		t.Fatalf("bob's machine got a job: %+v", js)
	}

	// The team has one item of its own, and its own newer kusheet.
	libWrite(t, alice, "skills/bench/SKILL.md", "---\nname: bench\ndescription: bench\n---\nb1\n")
	if _, _, err := alice.Publish("infer", "skill", "bench", false); err != nil {
		t.Fatal(err)
	}
	libWrite(t, alice, "skills/kusheet/SKILL.md", "---\nname: kusheet\ndescription: sheets\n---\nnew in team\n")
	if _, _, err := alice.Publish("infer", "skill", "kusheet", false); err != nil {
		t.Fatal(err)
	}
	teamSum := func(name string) string {
		ps, _ := alice.API.Packages("infer")
		for _, p := range ps {
			if p.Name == name {
				return p.SHA256
			}
		}
		return ""
	}
	kusheet := teamSum("kusheet")

	dry, err := alice.SyncTeamRepo(jobs[0], true)
	if err != nil || len(dry.Published) != 2 || len(dry.Proposed) != 2 {
		t.Fatalf("dry run %+v %v", dry, err)
	}
	if ps, _ := alice.API.Packages("infer"); len(ps) != 2 {
		t.Fatalf("dry run published: %+v", ps)
	}
	lines, err := alice.TeamRepoJobs(false)
	if err != nil || len(lines) != 1 {
		t.Fatalf("jobs %v %v", lines, err)
	}
	ps, _ := alice.API.Packages("infer")
	names := map[string]bool{}
	for _, p := range ps {
		names[p.Type+"/"+p.Name] = true
	}
	if !names["skill/kudoc"] || !names["agent/verifier"] || !names["skill/bench"] || len(ps) != 4 {
		t.Fatalf("team %v", names)
	}
	// The team wins: its kusheet is not replaced by the repository's.
	if teamSum("kusheet") != kusheet {
		t.Fatal("the repository's kusheet replaced the team's")
	}
	// The review: refs/for/master with the team's skills under skills/,
	// kusheet where the repository has it.
	out, err := exec.Command("git", "-C", bare, "show", "refs/for/master:skills/bench/SKILL.md").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "b1") {
		t.Fatalf("review content: %s %v (sync: %v)", out, err, lines)
	}
	out, err = exec.Command("git", "-C", bare, "show", "refs/for/master:skills/connectors/kusheet/SKILL.md").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "new in team") {
		t.Fatalf("review kusheet: %s %v", out, err)
	}
	msg, _ := exec.Command("git", "-C", bare, "log", "-1", "--format=%B", "refs/for/master").Output()
	if !strings.Contains(string(msg), "Change-Id: I") || !strings.Contains(string(msg), "skill bench") {
		t.Fatalf("commit message %q", msg)
	}
	r, _ := alice.API.TeamRepo("infer")
	if r.Status != "ok" || r.Review == "" || r.SyncedAt == 0 {
		t.Fatalf("status %+v", r)
	}
	// Not due again; a forced sync proposes nothing new and publishes nothing.
	if js, _ := alice.API.MyTeamRepos(); js[0].Due {
		t.Fatal("still due")
	}
	again, err := alice.SyncTeamRepo(jobs[0], false)
	if err != nil || len(again.Published) != 0 || len(again.Proposed) != 0 {
		t.Fatalf("second sync %+v %v", again, err)
	}
	// A maintainer asks for a sync; the repository changed meanwhile: kudoc
	// (changed only there) comes in, kusheet (the team's still pending) does not.
	write("skills/connectors/kudoc/SKILL.md", "---\nname: kudoc\ndescription: knowledge base\n---\nv2\n")
	write("skills/connectors/kusheet/SKILL.md", "---\nname: kusheet\ndescription: sheets\n---\nedited in repo\n")
	git(t, work, "commit", "-q", "-am", "v2")
	git(t, work, "push", "-q", "origin", "master")
	if err := bob.API.RequestTeamRepoSync("infer"); err != nil {
		t.Fatal(err)
	}
	if js, _ := alice.API.MyTeamRepos(); !js[0].Due {
		t.Fatal("asked, not due")
	}
	third, err := alice.SyncTeamRepo(jobs[0], false)
	if err != nil || len(third.Published) != 1 || third.Published[0] != "skill kudoc" {
		t.Fatalf("third sync %+v %v", third, err)
	}
	if teamSum("kusheet") != kusheet || !strings.Contains(strings.Join(third.Notes, "\n"), "kusheet changed in both") {
		t.Fatalf("kusheet replaced or not noted: %v", third.Notes)
	}
	// Only the syncing device reports.
	if err := bob.API.ReportTeamRepo("infer", jobs[0]); StatusOf(err) != 403 {
		t.Fatalf("bob reported: %v", err)
	}
}

func apiRepo(url, dev string) api.TeamRepo { return api.TeamRepo{URL: url, Device: dev} }
