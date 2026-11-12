package client

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/library"
)

// Team repositories, the machine's side. The team owner's chosen machine
// keeps a clone (with the owner's own git credentials) and, on each sync:
//
//  1. publishes the skills (skills/**/SKILL.md) and agents (agents/*.md)
//     that changed only in the repository into the team;
//  2. proposes the team's items that changed there (and the new ones) back
//     to the repository as one review: refs/for/<branch> (a CR, one
//     Change-Id kept until it is merged) or a branch hearth/team-<team>.
//
// The team wins: an item that differs and changed in the team (or was never
// synced, as on the first sync) keeps the team's version, which is proposed
// to the repository; the repository's copy never replaces it.
//
// Only skills/ and agents/ are read or written; names in the binding's
// exclude list are left alone on both sides. An item removed from the
// repository stays in the team (the sync says so). State in
// <home>/team-repos/<team>/state.json remembers what each side had at the
// last sync, which tells a change on one side from a change on the other.

type teamRepoState struct {
	Repo     map[string]string `json:"repo"`                // key -> repository content at the last sync
	Team     map[string]string `json:"team"`                // key -> team content at the last sync
	ChangeID string            `json:"change_id,omitempty"` // the open review's Change-Id
	Pending  map[string]string `json:"pending,omitempty"`   // key -> content proposed in it
	skipped  []string          // this sync: items that could not be proposed
}

// repoItem is one skill or agent found in the repository.
type repoItem struct {
	Type, Name string
	Rel        string // path in the repository (the skill's dir or the agent's .md)
	Sum, Desc  string
	data       []byte
	err        error // could not be packed (a credential in it)
}

func (i repoItem) key() string { return i.Type + "/" + i.Name }

// TeamRepoResult says what a sync did.
type TeamRepoResult struct {
	Commit    string
	Published []string // into the team
	Proposed  []string // in the review
	Review    string   // the review's URL or ref, when one was pushed
	Notes     []string // conflicts, removed items, items that could not go
	PushErr   string   // the review could not be pushed (the repository refused it)
}

// Status is what the sync reports to the server: failed when the review
// was refused, so the team page shows it.
func (r TeamRepoResult) Status() string {
	if r.PushErr != "" {
		return "failed"
	}
	return "ok"
}

// Summary is the one line a sync reports.
func (r TeamRepoResult) Summary() string {
	parts := []string{fmt.Sprintf("%d published to the team", len(r.Published)), fmt.Sprintf("%d proposed to the repository", len(r.Proposed))}
	s := strings.Join(parts, ", ")
	if r.PushErr != "" {
		s = "review not pushed: " + r.PushErr + "; " + s
	}
	if len(r.Notes) > 0 {
		s += "; " + strings.Join(r.Notes, "; ")
	}
	return s
}

func teamRepoDir(home, team string) string { return filepath.Join(home, "team-repos", team) }

func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// checkoutTeamRepo clones or updates the repository and checks out the
// branch's tip; returns the branch and the commit.
func checkoutTeamRepo(dir string, rep api.TeamRepo) (string, string, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return "", "", err
		}
		cmd := exec.Command("git", "clone", "-q", "--no-checkout", "--", rep.URL, dir)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("git clone %s: %v: %s", rep.URL, err, strings.TrimSpace(string(out)))
		}
	} else if _, err := gitRun(dir, "remote", "set-url", "origin", rep.URL); err != nil {
		return "", "", err
	}
	ref := rep.Ref
	if ref == "" {
		if out, err := gitRun(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
			ref = strings.TrimPrefix(out, "origin/")
		} else {
			ref = "master"
		}
	}
	if _, err := gitRun(dir, "fetch", "-q", "origin", "+refs/heads/"+ref+":refs/remotes/origin/"+ref); err != nil {
		return "", "", err
	}
	if _, err := gitRun(dir, "checkout", "-q", "-f", "-B", "hearth-sync", "origin/"+ref); err != nil {
		return "", "", err
	}
	if _, err := gitRun(dir, "clean", "-fdq", "--", "skills", "agents"); err != nil {
		return "", "", err
	}
	commit, err := gitRun(dir, "rev-parse", "HEAD")
	return ref, commit, err
}

// scanTeamRepo finds the skills and agents of a checkout.
func scanTeamRepo(dir string) ([]repoItem, []string) {
	var out []repoItem
	var notes []string
	seen := map[string]string{}
	skills := filepath.Join(dir, "skills")
	_ = filepath.WalkDir(skills, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != skills && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(skills, p)
			if strings.Count(rel, string(filepath.Separator)) > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		sd := filepath.Dir(p)
		name := filepath.Base(sd)
		rel, _ := filepath.Rel(dir, sd)
		rel = filepath.ToSlash(rel)
		if sd == skills || !library.ValidName(name) {
			return nil
		}
		if prev, ok := seen["skill/"+name]; ok {
			notes = append(notes, fmt.Sprintf("skill %s is in both %s and %s: %s is used", name, prev, rel, prev))
			return filepath.SkipDir
		}
		seen["skill/"+name] = rel
		it := repoItem{Type: "skill", Name: name, Rel: rel}
		if b, err := os.ReadFile(p); err == nil {
			it.Desc = library.SkillDescription(b)
		}
		it.data, it.Sum, it.err = bundleTree("skill", sd, false)
		out = append(out, it)
		return filepath.SkipDir // a skill's own files are not skills
	})
	entries, _ := os.ReadDir(filepath.Join(dir, "agents"))
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".md") || strings.HasPrefix(en.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "agents", en.Name()))
		if err != nil {
			continue
		}
		a, err := library.ParseAgent(b)
		if err != nil || a.Name+".md" != en.Name() {
			continue // not an agent file (or misnamed)
		}
		it := repoItem{Type: "agent", Name: a.Name, Rel: "agents/" + en.Name(), Desc: a.Description}
		tmp, err := os.MkdirTemp("", "hearth-agent-")
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(tmp, "AGENT.md"), b, 0o644); err == nil {
			it.data, it.Sum, it.err = bundleTree("agent", tmp, false)
		}
		os.RemoveAll(tmp)
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out, notes
}

// SyncTeamRepo runs one sync of a binding this machine holds. dryRun only
// says what it would publish and propose.
func (e *Engine) SyncTeamRepo(rep api.TeamRepo, dryRun bool) (TeamRepoResult, error) {
	var res TeamRepoResult
	base := teamRepoDir(e.Home, rep.Team)
	dir := filepath.Join(base, "repo")
	statePath := filepath.Join(base, "state.json")
	var st teamRepoState
	_ = loadJSON(statePath, &st)
	if st.Repo == nil {
		st.Repo, st.Team = map[string]string{}, map[string]string{}
	}
	ref, commit, err := checkoutTeamRepo(dir, rep)
	if err != nil {
		return res, err
	}
	res.Commit = commit
	items, notes := scanTeamRepo(dir)
	res.Notes = append(res.Notes, notes...)
	pkgs, err := e.API.Packages(rep.Team)
	if err != nil {
		return res, err
	}
	team := map[string]api.Package{}
	for _, p := range pkgs {
		team[p.Type+"/"+p.Name] = p
	}

	// 1. Repository → team, for what changed only in the repository (the
	// team wins).
	inRepo := map[string]repoItem{}
	var propose []string // keys whose team copy goes to the repository
	excluded := map[string]bool{}
	for _, n := range rep.Exclude {
		excluded[n] = true
	}
	var skippedNames []string
	for _, it := range items {
		k := it.key()
		inRepo[k] = it
		if excluded[it.Name] {
			skippedNames = append(skippedNames, it.Type+" "+it.Name)
			continue // neither way
		}
		if it.err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("%s %s not published: %s", it.Type, it.Name, firstLine(it.err.Error())))
			continue
		}
		t, inTeam := team[k]
		repoChanged := st.Repo[k] != it.Sum
		teamChanged := inTeam && t.SHA256 != st.Team[k] // never synced counts as changed
		switch {
		case inTeam && t.SHA256 == it.Sum:
			st.Repo[k], st.Team[k] = it.Sum, t.SHA256 // the same on both sides
		case !inTeam && !repoChanged:
			// Removed from the team since the last sync: not brought back.
		case !inTeam || !teamChanged:
			// New in the repository, or changed only there.
			if !dryRun {
				if _, err := e.API.Publish(rep.Team, it.Type, it.Name, it.Desc, it.Sum, it.data); err != nil {
					res.Notes = append(res.Notes, fmt.Sprintf("%s %s not published: %s", it.Type, it.Name, firstLine(err.Error())))
					continue
				}
			}
			res.Published = append(res.Published, it.Type+" "+it.Name)
			st.Repo[k], st.Team[k] = it.Sum, it.Sum
		default:
			// Changed in the team (or never synced): the team's version
			// stays and goes to the repository. st.Team keeps the old sum
			// so it is proposed again until the repository has it.
			if repoChanged && st.Repo[k] != "" {
				res.Notes = append(res.Notes, fmt.Sprintf("%s %s changed in both: the team's version is kept and proposed", it.Type, it.Name))
			} else if st.Team[k] == "" && st.Repo[k] == "" {
				res.Notes = append(res.Notes, fmt.Sprintf("%s %s differs from the repository: the team's version is kept and proposed", it.Type, it.Name))
			}
			st.Repo[k] = it.Sum
			propose = append(propose, k)
		}
	}
	for k, t := range team {
		if _, ok := inRepo[k]; ok {
			continue
		}
		if excluded[t.Name] {
			skippedNames = append(skippedNames, t.Type+" "+t.Name)
			continue
		}
		if st.Repo[k] != "" {
			// It was in the repository and was removed there.
			if t.SHA256 == st.Team[k] {
				res.Notes = append(res.Notes, fmt.Sprintf("%s %s was removed from the repository; still in the team (hearth team unshare %s %s)", t.Type, t.Name, rep.Team, t.Name))
				continue
			}
		}
		propose = append(propose, k) // new in the team
	}
	sort.Strings(propose)
	if len(skippedNames) > 0 {
		sort.Strings(skippedNames)
		res.Notes = append(res.Notes, "not synced (excluded): "+strings.Join(skippedNames, ", "))
	}

	// 2. Team → repository, as one review.
	want := map[string]string{}
	for _, k := range propose {
		want[k] = team[k].SHA256
	}
	if dryRun {
		for _, k := range propose {
			res.Proposed = append(res.Proposed, strings.Replace(k, "/", " ", 1))
		}
		return res, nil
	}
	if len(propose) == 0 {
		st.ChangeID, st.Pending = "", nil // merged (or nothing to propose)
	} else if rep.Push != "off" && !sameSums(want, st.Pending) {
		review, proposed, err := e.proposeTeamItems(dir, rep, ref, propose, team, inRepo, &st)
		res.Notes = append(res.Notes, st.skipped...)
		if err != nil {
			res.PushErr = err.Error()
		} else if len(proposed) > 0 {
			res.Review, res.Proposed = review, proposed
			st.Pending = want
		}
	} else if rep.Push != "off" {
		res.Notes = append(res.Notes, fmt.Sprintf("%d item(s) already proposed, waiting for the review", len(propose)))
	}
	if err := os.MkdirAll(base, 0o700); err == nil {
		_ = saveJSON(statePath, st)
	}
	return res, nil
}

func sameSums(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

var reviewURLRE = regexp.MustCompile(`https?://\S+`)

// proposeTeamItems writes the team's copies of keys into the repository on
// a fresh branch from the tip, commits as the owner and pushes the review.
func (e *Engine) proposeTeamItems(dir string, rep api.TeamRepo, ref string, keys []string, team map[string]api.Package, inRepo map[string]repoItem, st *teamRepoState) (string, []string, error) {
	defer gitRun(dir, "checkout", "-q", "-f", "hearth-sync")
	if _, err := gitRun(dir, "checkout", "-q", "-f", "-B", "hearth-propose", "origin/"+ref); err != nil {
		return "", nil, err
	}
	var done []string
	for _, k := range keys {
		p := team[k]
		data, _, sum, err := e.API.GetPackage(rep.Team, p.Type, p.Name, p.Version)
		if err != nil {
			st.skipped = append(st.skipped, fmt.Sprintf("%s %s: %v", p.Type, p.Name, err))
			continue
		}
		tmpBase, err := os.MkdirTemp("", "hearth-pkg-")
		if err != nil {
			return "", nil, err
		}
		tmp := filepath.Join(tmpBase, "x")
		got, err := library.Extract(data, tmp)
		if err != nil || got != sum {
			os.RemoveAll(tmpBase)
			st.skipped = append(st.skipped, fmt.Sprintf("%s %s: could not unpack (%v)", p.Type, p.Name, err))
			continue
		}
		if fs, _ := library.ScanSecrets(tmp); len(fs) > 0 {
			os.RemoveAll(tmpBase)
			st.skipped = append(st.skipped, fmt.Sprintf("%s %s: looks like it holds a credential, not proposed", p.Type, p.Name))
			continue // never into a repository
		}
		switch p.Type {
		case "skill":
			rel := rep.NewDir + "/" + p.Name
			if it, ok := inRepo[k]; ok {
				rel = it.Rel
			}
			dst := filepath.Join(dir, filepath.FromSlash(rel))
			_ = os.RemoveAll(dst)
			if _, err := library.CopyTree(tmp, dst); err != nil {
				os.RemoveAll(tmpBase)
				return "", nil, err
			}
		case "agent":
			b, err := os.ReadFile(filepath.Join(tmp, "AGENT.md"))
			if err != nil {
				os.RemoveAll(tmpBase)
				continue
			}
			_ = os.MkdirAll(filepath.Join(dir, "agents"), 0o755)
			if err := os.WriteFile(filepath.Join(dir, "agents", p.Name+".md"), b, 0o644); err != nil {
				os.RemoveAll(tmpBase)
				return "", nil, err
			}
		}
		os.RemoveAll(tmpBase)
		done = append(done, p.Type+" "+p.Name)
	}
	if len(done) == 0 {
		return "", nil, nil
	}
	if _, err := gitRun(dir, "add", "-A", "--", "skills", "agents"); err != nil {
		return "", nil, err
	}
	if out, _ := gitRun(dir, "status", "--porcelain", "--", "skills", "agents"); out == "" {
		return "", nil, nil // the repository has them already
	}
	if st.ChangeID == "" {
		b := make([]byte, 20)
		_, _ = rand.Read(b)
		st.ChangeID = "I" + hex.EncodeToString(b)
	}
	msg := fmt.Sprintf("hearth: sync skills and agents from team %s\n\n", rep.Team)
	if rep.CommitPrefix != "" {
		msg = rep.CommitPrefix + " " + msg // an issue id the repository's hooks require
	}
	for _, d := range done {
		msg += "- " + d + "\n"
	}
	if rep.Push == "cr" {
		msg += "\nChange-Id: " + st.ChangeID + "\n"
	}
	args := []string{"commit", "-q", "-m", msg}
	if out, _ := gitRun(dir, "config", "user.email"); out == "" {
		user, _, _ := strings.Cut(e.Cfg.Email, "@")
		args = append([]string{"-c", "user.email=" + e.Cfg.Email, "-c", "user.name=" + user}, args...)
	}
	if _, err := gitRun(dir, args...); err != nil {
		return "", nil, err
	}
	target := "HEAD:refs/for/" + ref
	if rep.Push == "branch" {
		target = "+HEAD:refs/heads/hearth/team-" + rep.Team
	}
	out, err := gitRun(dir, "push", "origin", target)
	if err != nil {
		if strings.Contains(out, "no new changes") {
			return "", nil, nil
		}
		return "", nil, errors.New(pushRefusal(out, rep))
	}
	review := strings.TrimPrefix(target, "+HEAD:")
	if m := reviewURLRE.FindString(out); m != "" {
		review = m
	}
	return review, done, nil
}

// TeamRepoJobs syncs the bindings this machine holds that are due, and
// reports each result. Returns what was done (for the daemon's log).
func (e *Engine) TeamRepoJobs(force bool) ([]string, error) {
	reps, err := e.API.MyTeamRepos()
	if err != nil {
		if StatusOf(err) == 404 {
			return nil, nil // an older server
		}
		return nil, err
	}
	var log []string
	for _, rep := range reps {
		if !rep.Due && !force {
			continue
		}
		r, err := e.SyncTeamRepo(rep, false)
		st := api.TeamRepo{Status: r.Status(), Detail: r.Summary(), Commit: r.Commit, Review: r.Review}
		if err != nil {
			st.Status, st.Detail = "failed", err.Error()
		}
		if rerr := e.API.ReportTeamRepo(rep.Team, st); rerr != nil && err == nil {
			err = rerr
		}
		line := "team " + rep.Team + " repository: " + st.Detail
		if r.Review != "" {
			line += " (" + r.Review + ")"
		}
		log = append(log, line)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log[len(log)-1] = "team " + rep.Team + " repository: " + err.Error()
		}
	}
	return log, nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(l)
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// pushRefusal turns git push's output into one readable line: the
// rejection and the hook's message, without the server's banner, colours
// and blank lines; plus what to set when the hook wants an issue id.
func pushRefusal(out string, rep api.TeamRepo) string {
	out = ansiRE.ReplaceAllString(out, "")
	lines := strings.Split(out, "\n")
	start := 0
	for i, l := range lines {
		if strings.Contains(l, "[remote rejected]") || strings.Contains(l, "[rejected]") {
			start = i
			break
		}
	}
	var keep []string
	for _, l := range lines[start:] {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "remote:"))
		if l == "" || strings.Trim(l, "=<>. ") == "" || strings.HasPrefix(l, "error: failed to push") {
			continue
		}
		keep = append(keep, l)
		if len(keep) == 6 {
			break
		}
	}
	msg := strings.Join(keep, " ")
	if msg == "" {
		msg = firstLine(out)
	}
	low := strings.ToLower(out)
	if rep.CommitPrefix == "" && (strings.Contains(low, "card") || strings.Contains(low, "issue")) {
		msg += fmt.Sprintf(" (if the repository needs an issue id in the commit message: hearth team repo set %s <url> --machine <machine> --commit-prefix <id>)", rep.Team)
	}
	return msg
}
