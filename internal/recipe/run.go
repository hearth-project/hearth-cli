package recipe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hearth-project/hearth-cli/internal/machine"
	"github.com/hearth-project/hearth-cli/internal/paths"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Host gives the runner access to secrets and library files.
type Host interface {
	Secret(name string) ([]byte, bool, error)
	SetSecret(name string, value []byte) error
	LibraryFile(rel string) ([]byte, error)
}

// Mode selects which steps a run performs.
type Mode int

const (
	Apply   Mode = iota // setup: tool, file, env, run, service, check, manual
	Doctor              // checks and manual status only, no changes
	Save                // capture steps
	Up                  // services only (after a container restart)
	Restore             // setup: files (credentials, configs) and env only; no commands
)

// Options for Run.
type Options struct {
	Mode Mode
	// OnlyKeys limits a run to these steps (StepKey); others are left out
	// of the results.
	OnlyKeys map[string]bool
	Approve  bool   // approve commands shown as needs-approval
	DryRun   bool   // report only
	StateDir string // ~/.hearth: approvals, env script, backups
	Optional bool   // also run optional checks
	Timeout  time.Duration
	OnlyDue  bool // Save: honour `every` (daemon)
	Now      func() time.Time
	// Machine is this machine's facts and active profiles (nil: OS and
	// architecture only, no profiles).
	Machine  *machine.Facts
	Profiles []string
	RCFile   string // shell rc that sources the env script (default DefaultRCFile: ~/.zshrc for zsh, else ~/.bashrc)
	// Approved are approvals given on the user's other machines (synced
	// end-to-end encrypted): the same command approved once runs everywhere.
	Approved map[string]bool
	// MarkDone marks these pending manual steps (ManualKey) done on this
	// machine; "*" = every pending one.
	MarkDone map[string]bool
}

// Result.Key of a manual step identifies it for `hearth recipe done`.

// ManualKey identifies a manual step by recipe and text (stable when steps
// are reordered; a changed text asks again).
func ManualKey(recipe string, s Step) string {
	h := sha256.Sum256([]byte(s.Text))
	return recipe + "/" + hex.EncodeToString(h[:])[:10]
}

func doneFile(dir string) string { return filepath.Join(dir, "recipe-done.json") }

func loadDone(dir string) map[string]bool {
	m := map[string]bool{}
	if b, err := os.ReadFile(doneFile(dir)); err == nil {
		var list []string
		if json.Unmarshal(b, &list) == nil {
			for _, k := range list {
				m[k] = true
			}
		}
	}
	return m
}

func saveDone(dir string, m map[string]bool) error {
	list := make([]string, 0, len(m))
	for k := range m {
		list = append(list, k)
	}
	sort.Strings(list)
	b, _ := json.MarshalIndent(list, "", "  ")
	return os.WriteFile(doneFile(dir), b, 0o600)
}

// LoadApprovals returns the approvals recorded in dir (recipe/fingerprint).
func LoadApprovals(dir string) map[string]bool { return loadApprovals(dir) }

// Result is one reported line.
type Result struct {
	Recipe string
	Step   string
	Op     string // ok, applied, skipped, warn, needs-approval, failed, pending
	Detail string
	Key    string // manual steps: ManualKey, for `hearth recipe done`
}

// Bad: the step failed. Warnings are reported but do not fail a run.
func (r Result) Bad() bool { return r.Op == "failed" }

// Arch returns uname-style architecture names used in recipes.
func Arch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return runtime.GOARCH
}

func stepLabel(i int, s Step) string {
	l := fmt.Sprintf("%d:%s", i+1, s.Type)
	switch {
	case s.Name != "":
		l += " " + s.Name
	case s.Type == "file":
		l += " " + strings.Join(s.To, ",")
	case s.Type == "manual":
		l += " " + firstLine(s.Text, 50)
	case s.Run != "":
		l += " " + firstLine(s.Run, 50)
	}
	return l
}

func firstLine(s string, n int) string {
	s = strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// fingerprint covers every command a step may execute.
func (s Step) fingerprint() string {
	sha, _ := json.Marshal(s.SHA256)
	h := sha256.Sum256([]byte(strings.Join([]string{s.Type, s.Run, s.Unless, s.DoneWhen, s.StdinSecret, s.Secret,
		s.URL, string(sha), s.NPM, s.Registry, s.Node, s.Wrapper, s.InstallDir, s.Bin, s.BinDest, s.Check, s.Start, s.Health}, "\x00")))
	return hex.EncodeToString(h[:])
}

func (s Step) hasCommands() bool { return s.Run != "" || s.Unless != "" || s.DoneWhen != "" }

type approvals map[string]bool

func loadApprovals(dir string) approvals {
	a := approvals{}
	b, err := os.ReadFile(filepath.Join(dir, "recipe-approved.json"))
	if err == nil {
		var list []string
		if json.Unmarshal(b, &list) == nil {
			for _, x := range list {
				a[x] = true
			}
		}
	}
	return a
}

func (a approvals) save(dir string) error {
	var list []string
	for k := range a {
		list = append(list, k)
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	return os.WriteFile(filepath.Join(dir, "recipe-approved.json"), b, 0o600)
}

// Run executes recipes in phase order and returns what happened.
func Run(recipes []*Recipe, h Host, opt Options) ([]Result, error) {
	if opt.Timeout == 0 {
		opt.Timeout = 5 * time.Minute
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if err := os.MkdirAll(opt.StateDir, 0o700); err != nil {
		return nil, err
	}
	if opt.Machine == nil {
		opt.Machine = &machine.Facts{OS: runtime.GOOS, Arch: Arch()}
	}
	recipes = withRecipeWhen(recipes)
	r := &runner{h: h, opt: opt, ap: loadApprovals(opt.StateDir), done: loadDone(opt.StateDir), stamp: opt.Now().Format("20060102-150405")}
	phases := map[Mode][]string{
		Apply:   {"tool", "file", "env", "run", "service", "check", "manual"},
		Doctor:  {"check", "manual"},
		Save:    {"capture"},
		Up:      {"service"},
		Restore: {"file", "env"},
	}[opt.Mode]
	tools := map[string]Step{}
	for _, rc := range recipes {
		for _, s := range rc.Steps {
			if s.Type == "tool" {
				tools[s.Name] = s
			}
		}
	}
	var envSteps []scopedEnv
	failed := map[string]bool{} // across phases: a service can depend on a tool
	for _, phase := range phases {
		items, cycle := ordered(recipes, phase)
		if cycle != "" {
			r.out = append(r.out, Result{Recipe: "*", Step: phase, Op: "failed", Detail: "depends_on cycle: " + cycle})
			continue
		}
		for _, it := range items {
			s := it.step
			if opt.OnlyKeys != nil && !opt.OnlyKeys[StepKey(it.recipe, s)] {
				continue
			}
			res := Result{Recipe: it.recipe, Step: stepLabel(it.index, s), Key: StepKey(it.recipe, s)}
			if skip, why := r.skip(s); skip {
				res.Op, res.Detail = "skipped", why
			} else if dep := firstFailed(s.DependsOn, failed); dep != "" {
				res.Op, res.Detail = "skipped", "dependency "+dep+" is not ready"
			} else if s.Type == "env" {
				envSteps = append(envSteps, scopedEnv{it.recipe, s})
				continue
			} else if s.Type == "tool" {
				res.Op, res.Detail = r.tool(it.recipe, s, tools)
			} else if s.Type == "service" {
				res.Op, res.Detail = r.service(it.recipe, s)
			} else {
				res.Op, res.Detail = r.step(it.recipe, s)
			}
			if s.Name != "" && res.Op != "ok" && res.Op != "applied" {
				failed[s.Name] = true
			}
			r.out = append(r.out, res)
		}
		if phase == "env" && len(envSteps) > 0 {
			op, detail := r.env(envSteps)
			r.out = append(r.out, Result{Recipe: "*", Step: "env", Op: op, Detail: detail})
		}
	}
	if !opt.DryRun {
		if err := r.ap.save(opt.StateDir); err != nil {
			return r.out, err
		}
	}
	if len(opt.MarkDone) > 0 {
		if err := saveDone(opt.StateDir, r.done); err != nil {
			return r.out, err
		}
	}
	return r.out, nil
}

type runner struct {
	h     Host
	opt   Options
	ap    approvals
	done  map[string]bool // manual steps marked done on this machine
	stamp string
	out   []Result
}

func (r *runner) skip(s Step) (bool, string) {
	if ok, why := r.machineMatches(s.When); !ok {
		return true, why
	}
	if s.WhenSecret != "" {
		if _, ok, err := r.h.Secret(s.WhenSecret); err != nil {
			return true, "cannot read secrets: " + err.Error()
		} else if !ok {
			return true, "no secret " + s.WhenSecret
		}
	}
	if s.UnlessSecret != "" {
		if _, ok, err := r.h.Secret(s.UnlessSecret); err != nil {
			return true, "cannot read secrets: " + err.Error()
		} else if ok {
			return true, "secret " + s.UnlessSecret + " exists"
		}
	}
	if s.Type == "check" && s.Optional && !r.opt.Optional {
		return true, "optional (use --all)"
	}
	return false, ""
}

// approved gates every step that executes commands (design doc 14.3).
func (r *runner) approved(recipe string, s Step) bool {
	key := recipe + "/" + s.fingerprint()
	if r.ap[key] {
		return true
	}
	if r.opt.Approved[key] {
		r.ap[key] = true
		return true
	}
	if r.opt.Approve && !r.opt.DryRun {
		r.ap[key] = true
		return true
	}
	return false
}

func approvalDetail(s Step) string {
	var parts []string
	for _, c := range []string{s.Unless, s.Run, s.DoneWhen} {
		if c != "" {
			parts = append(parts, "$ "+c)
		}
	}
	return strings.Join(parts, "  ")
}

// sh runs a command with a timeout; stdin may carry a secret.
func (r *runner) sh(cmdline string, stdin []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.opt.Timeout)
	defer cancel()
	// ${WORKSPACE} means the same thing in commands as in file/env targets;
	// the shell only knows HEARTH_WORKSPACE, so expand it here.
	if ws := paths.Workspace(); ws != "" {
		cmdline = strings.ReplaceAll(cmdline, "${WORKSPACE}", ws)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
	cmd.Env = append(os.Environ(), "HEARTH_ARCH="+Arch())
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("timed out after %s", r.opt.Timeout)
	}
	out := buf.Bytes()
	if len(out) > 64<<10 {
		out = out[len(out)-64<<10:]
	}
	return out, err
}

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = "…" + s[len(s)-300:]
	}
	return s
}

func (r *runner) step(recipe string, s Step) (string, string) {
	switch s.Type {
	case "file":
		return r.file(s)
	case "manual":
		key := ManualKey(recipe, s)
		if r.done[key] {
			return "ok", "marked done on this machine"
		}
		if r.opt.MarkDone[key] || r.opt.MarkDone["*"] {
			r.done[key] = true
			return "ok", "marked done"
		}
		// done_when is a check: it also runs in a dry run once approved.
		if s.DoneWhen != "" && r.approved(recipe, s) {
			if _, err := r.sh(s.DoneWhen, nil); err == nil {
				return "ok", "done"
			}
		}
		d := s.Text
		if s.TTY {
			d += "  (needs an interactive terminal)"
		}
		return "pending", d
	}
	if !r.approved(recipe, s) {
		return "needs-approval", approvalDetail(s)
	}
	if r.opt.DryRun {
		return "would-run", approvalDetail(s)
	}
	switch s.Type {
	case "check":
		out, err := r.sh(s.Run, nil)
		if err != nil {
			return "warn", fmt.Sprintf("%v: %s", err, tail(out))
		}
		if s.Expect != "" && !regexp.MustCompile(s.Expect).Match(out) {
			return "warn", fmt.Sprintf("output does not match %q: %s", s.Expect, tail(out))
		}
		return "ok", ""
	case "run":
		if s.Unless != "" {
			if _, err := r.sh(s.Unless, nil); err == nil {
				return "ok", "already satisfied"
			}
		}
		var stdin []byte
		if s.StdinSecret != "" {
			v, ok, err := r.h.Secret(s.StdinSecret)
			if err != nil || !ok {
				return "failed", "secret " + s.StdinSecret + " unavailable"
			}
			stdin = v
		}
		out, err := r.sh(s.Run, stdin)
		if err != nil {
			return "failed", fmt.Sprintf("%v: %s", err, tail(out))
		}
		return "applied", ""
	case "capture":
		return r.capture(recipe, s)
	}
	return "failed", "unsupported step"
}

func (r *runner) capture(recipe string, s Step) (string, string) {
	statePath := filepath.Join(r.opt.StateDir, "capture-state.json")
	state := map[string]int64{}
	if b, err := os.ReadFile(statePath); err == nil {
		json.Unmarshal(b, &state)
	}
	key := recipe + "/" + s.fingerprint()
	if r.opt.OnlyDue && s.Every != "" {
		if d, err := time.ParseDuration(s.Every); err == nil && r.opt.Now().Sub(time.Unix(state[key], 0)) < d {
			return "skipped", "not due"
		}
	}
	out, err := r.sh(s.Run, nil)
	if err != nil {
		return "failed", fmt.Sprintf("%v: %s", err, tail(out))
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return "warn", "command printed nothing; secret not updated"
	}
	state[key] = r.opt.Now().Unix()
	b, _ := json.Marshal(state)
	os.WriteFile(statePath, b, 0o600)
	if cur, ok, _ := r.h.Secret(s.Secret); ok && bytes.Equal(cur, out) {
		return "ok", "unchanged"
	}
	if err := r.h.SetSecret(s.Secret, out); err != nil {
		return "failed", err.Error()
	}
	return "applied", "secret " + s.Secret + " updated"
}

func (r *runner) file(s Step) (string, string) {
	var content []byte
	var err error
	if s.Secret != "" {
		var ok bool
		content, ok, err = r.h.Secret(s.Secret)
		if err == nil && !ok {
			return "failed", "no secret named " + s.Secret
		}
	} else {
		content, err = r.h.LibraryFile(s.Library)
	}
	if err != nil {
		return "failed", err.Error()
	}
	mode, _ := s.mode()
	var done, kept, warned, shared []string
	for _, t := range s.To {
		if !s.SharedOK && sharedHere(t) {
			shared = append(shared, t)
			continue
		}
		p := ExpandHome(t)
		cur, err := os.ReadFile(p)
		switch {
		case err == nil && bytes.Equal(cur, content):
			if fi, _ := os.Stat(p); fi != nil && fi.Mode().Perm() != mode && !r.opt.DryRun {
				os.Chmod(p, mode)
			}
			continue
		case err == nil && (s.Policy == "" || s.Policy == "if-absent"):
			kept = append(kept, t)
			continue
		case err == nil && s.Policy == "warn":
			warned = append(warned, t)
			continue
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return "failed", err.Error()
		}
		if r.opt.DryRun {
			done = append(done, t)
			continue
		}
		if err == nil { // overwrite: keep a backup
			bak := filepath.Join(r.opt.StateDir, "backup", r.stamp, strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", "__"))
			if err := os.MkdirAll(filepath.Dir(bak), 0o700); err != nil {
				return "failed", err.Error()
			}
			if err := os.WriteFile(bak, cur, 0o600); err != nil {
				return "failed", err.Error()
			}
		}
		if err := writeAtomic(p, content, mode); err != nil {
			return "failed", err.Error()
		}
		done = append(done, t)
	}
	switch {
	case len(shared) > 0 && len(done) == 0:
		return "skipped", fmt.Sprintf("%s: no workspace on this machine, so it would be the shared %s; run `hearth init <your dir>` and restore again", strings.Join(shared, ", "), ExpandHome(shared[0]))
	case len(warned) > 0:
		return "warn", fmt.Sprintf("%s differ from the stored version; kept local. Use policy = \"overwrite\" or update the source", strings.Join(warned, ", "))
	case len(done) > 0:
		d := "wrote " + strings.Join(done, ", ")
		if len(kept) > 0 {
			d += "; kept existing " + strings.Join(kept, ", ")
		}
		if len(shared) > 0 {
			d += "; skipped " + strings.Join(shared, ", ") + " (no workspace here)"
		}
		return "applied", d
	case len(kept) > 0:
		return "ok", "kept existing " + strings.Join(kept, ", ")
	}
	return "ok", ""
}

func writeAtomic(p string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".hearth-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

type item struct {
	recipe string
	index  int
	step   Step
}

func firstFailed(deps []string, failed map[string]bool) string {
	for _, d := range deps {
		if failed[d] {
			return d
		}
	}
	return ""
}

// ordered returns a phase's steps in recipe order, moved after the named
// steps they depend on (dependencies outside the phase are ignored).
func ordered(recipes []*Recipe, phase string) ([]item, string) {
	var all []item
	names := map[string]bool{}
	for _, rc := range recipes {
		for i, s := range rc.Steps {
			if s.Type == phase {
				all = append(all, item{rc.Name, i, s})
				if s.Name != "" {
					names[s.Name] = true
				}
			}
		}
	}
	var out []item
	done := map[string]bool{}
	emitted := make([]bool, len(all))
	for len(out) < len(all) {
		progress := false
		for i, it := range all {
			if emitted[i] {
				continue
			}
			ready := true
			for _, d := range it.step.DependsOn {
				if names[d] && !done[d] {
					ready = false
				}
			}
			if ready {
				emitted[i], progress = true, true
				out = append(out, it)
				if it.step.Name != "" {
					done[it.step.Name] = true
				}
			}
		}
		if !progress {
			var stuck []string
			for i, it := range all {
				if !emitted[i] {
					stuck = append(stuck, it.step.Name)
				}
			}
			return nil, strings.Join(stuck, ", ")
		}
	}
	return out, ""
}

// withRecipeWhen copies recipes so each step carries its recipe's when
// conditions (step keys win).
func withRecipeWhen(rs []*Recipe) []*Recipe {
	out := make([]*Recipe, len(rs))
	for i, rc := range rs {
		if len(rc.When) == 0 {
			out[i] = rc
			continue
		}
		c := *rc
		c.Steps = make([]Step, len(rc.Steps))
		for j, s := range rc.Steps {
			w := map[string]string{}
			for k, v := range rc.When {
				w[k] = v
			}
			for k, v := range s.When {
				w[k] = v
			}
			s.When = w
			c.Steps[j] = s
		}
		out[i] = &c
	}
	return out
}

// machineMatches evaluates when conditions against this machine.
func (r *runner) machineMatches(w map[string]string) (bool, string) {
	f := r.opt.Machine
	for _, k := range sortedKeys(w) {
		cond := w[k]
		if k == "profile" {
			if !profileMatch(r.opt.Profiles, cond) {
				p := strings.Join(r.opt.Profiles, ",")
				if p == "" {
					p = "none"
				}
				return false, "for profile " + cond + " (this machine: " + p + ")"
			}
			continue
		}
		if !f.MatchValue(k, cond) {
			return false, fmt.Sprintf("for %s %s (this machine: %s)", k, cond, factOf(f, k))
		}
	}
	return true, ""
}

func profileMatch(active []string, cond string) bool {
	vals := active
	if len(vals) == 0 {
		vals = []string{"none"}
	}
	f := machine.Facts{Labels: vals} // reuse list matching semantics
	return f.MatchValue("label", cond)
}

func factOf(f *machine.Facts, k string) string {
	switch k {
	case "os":
		return f.OS
	case "arch":
		return f.Arch
	case "gpu":
		if f.GPU == "" {
			return "none"
		}
		return f.GPU
	case "hostname":
		return f.Hostname
	case "label":
		return strings.Join(f.Labels, ",")
	}
	return ""
}
