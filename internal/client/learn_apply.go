package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/hearth-project/hearth-cli/internal/backup"
	"github.com/hearth-project/hearth-cli/internal/library"
	mach "github.com/hearth-project/hearth-cli/internal/machine"
	"github.com/hearth-project/hearth-cli/internal/paths"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// LoadLearned returns the "learned" recipe (empty if it does not exist).
func LoadLearned(home string) (*recipe.Recipe, error) {
	b, err := os.ReadFile(filepath.Join(RecipesDir(home), LearnedRecipe+".toml"))
	if os.IsNotExist(err) {
		return &recipe.Recipe{Name: LearnedRecipe, Description: "Learned from this user's machines by `hearth learn`"}, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := recipe.Parse(b)
	if err == nil && upgradeLearned(r) {
		err = SaveRecipe(home, r) // a migration: keep it
	}
	return r, err
}

// upgradeLearned replaces steps recorded by older versions that do not
// work any more: codex-mixin's export / import take a file path.
func upgradeLearned(r *recipe.Recipe) bool {
	cur := map[string]recipe.Step{}
	for _, s := range codexMixinSteps() {
		cur[s.Name] = s
	}
	at := -1
	for i, s := range r.Steps {
		if c, ok := cur[s.Name]; ok && at < 0 && (c.Run != s.Run || c.Text != s.Text || c.StdinSecret != s.StdinSecret) {
			at = i
		}
	}
	if at < 0 {
		return false
	}
	var out []recipe.Step
	for i, s := range r.Steps {
		if i == at {
			out = append(out, codexMixinSteps()...)
		}
		if !strings.HasPrefix(s.Name, "codex-mixin-") {
			out = append(out, s)
		}
	}
	r.Steps = out
	return true
}

// SaveRecipe validates and writes a recipe into the library.
func SaveRecipe(home string, r *recipe.Recipe) error {
	if err := r.Validate(); err != nil {
		return err
	}
	b, err := toml.Marshal(r)
	if err != nil {
		return err
	}
	header := "# Maintained by `hearth learn` (and accepted proposals); safe to edit by hand.\n"
	if err := os.MkdirAll(RecipesDir(home), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(RecipesDir(home), r.Name+".toml"), append([]byte(header), b...), 0o644)
}

// MergeSteps adds or replaces steps by name; unnamed steps are appended
// unless an identical step exists.
func MergeSteps(r *recipe.Recipe, steps []recipe.Step) {
	for _, s := range steps {
		replaced := false
		for i := range r.Steps {
			if s.Name != "" && r.Steps[i].Name == s.Name {
				r.Steps[i], replaced = s, true
				break
			}
		}
		if !replaced {
			r.Steps = append(r.Steps, s)
		}
	}
}

// Accept records a finding: secrets are uploaded, library files copied,
// steps merged into the learned recipe (saved by the caller via SaveRecipe).
func (e *Engine) Accept(f Finding, learned *recipe.Recipe) (string, error) {
	var did []string
	if f.Kind == "credential" || f.Kind == "identity" {
		// Uploading a credential is a choice the user's selection records.
		cfg := BackupSelection(e.Home)
		if !cfg.Enabled(backup.Credentials) {
			if _, err := EnsureBackupConfig(e.Home); err != nil {
				return "", err
			}
			if err := backup.SetCategory(BackupConfigPath(e.Home), backup.Credentials, true); err != nil {
				return "", err
			}
			did = append(did, "@credentials on")
		}
	}
	files := append([]secretFile(nil), f.secretFiles...)
	if f.secretPath != "" {
		files = append(files, secretFile{name: f.secretName, path: f.secretPath})
	}
	for _, sf := range files {
		v, err := os.ReadFile(sf.path)
		if err != nil {
			return "", err
		}
		if err := e.SetSecret(Secret{Name: sf.name, Value: v}); err != nil {
			return "", err
		}
		did = append(did, "secret "+sf.name)
	}
	if f.libFile != "" {
		v, err := os.ReadFile(f.srcFile)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(LibraryDir(e.Home), filepath.FromSlash(f.libFile))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, v, 0o644); err != nil {
			return "", err
		}
		did = append(did, "library "+f.libFile)
	}
	if f.tool != nil {
		m, err := LoadTools(e.Home)
		if err != nil {
			return "", err
		}
		if err := m.Set(*f.tool); err != nil {
			return "", err
		}
		if err := SaveTools(e.Home, m); err != nil {
			return "", err
		}
		did = append(did, "tool "+f.tool.Name+"@"+f.tool.Version)
	}
	if f.mcpImport != "" {
		added, _, err := e.ImportMCP(f.mcpImport, false)
		if err != nil {
			return "", err
		}
		did = append(did, fmt.Sprintf("mcp %v", added))
	}
	if f.wsFile != "" {
		if err := AddWorkspaceFiles(e.Home, f.wsFile); err != nil {
			return "", err
		}
		did = append(did, "workspace file ${WORKSPACE}/"+f.wsFile+" (synced encrypted, restored into the workspace)")
	}
	if f.libImport != nil {
		acts, err := library.Import(library.Library{Root: LibraryDir(e.Home)}, *f.libImport, RenderManifest(e.Home), false)
		if err != nil {
			return "", err
		}
		n := 0
		for _, a := range acts {
			if a.Op == "import" {
				n++
			}
		}
		did = append(did, fmt.Sprintf("library: %d item(s) imported from %s", n, paths.Contract(f.libImport.Root)))
	}
	if len(f.steps) > 0 {
		MergeSteps(learned, f.steps)
		did = append(did, fmt.Sprintf("%d recipe step(s)", len(f.steps)))
	}
	return strings.Join(did, ", "), nil
}

// UserStep turns a free-text answer into a step: "$ cmd" becomes a run
// step (approved per machine like any other), anything else a manual step.
func UserStep(answer string, n int) (recipe.Step, bool) {
	a := strings.TrimSpace(answer)
	if a == "" {
		return recipe.Step{}, false
	}
	name := fmt.Sprintf("user-%d", n)
	if cmd, ok := strings.CutPrefix(a, "$ "); ok {
		return recipe.Step{Type: "run", Name: name, Run: contractWords(strings.TrimSpace(cmd))}, true
	}
	return recipe.Step{Type: "manual", Name: name, Text: a}, true
}

// LearnStatus reports for each finding ID whether it is already part of the
// learned recipe: "new", "known", or "changed" when the stored secret
// differs from the file on this machine. Repeated runs then only ask about
// what is new or changed. Secrets are fetched once.
func (e *Engine) LearnStatus(fs []Finding, learned *recipe.Recipe) map[string]string {
	have := map[string]bool{}
	for _, s := range learned.Steps {
		have[s.Name] = true
		if base, _, ok := strings.Cut(s.Name, "@"); ok && profileActive(e.Home, s.When["profile"]) {
			have[base] = true // recorded for this kind of machine
		}
	}
	var stored map[string][]byte
	out := map[string]string{}
	for _, f := range fs {
		st := "known"
		for _, s := range f.steps {
			if !have[s.Name] {
				st = "new"
			}
		}
		if len(f.steps) == 0 {
			st = "new" // tools, MCP: Discover omits them once recorded
		}
		files := append([]secretFile(nil), f.secretFiles...)
		if f.secretPath != "" {
			files = append(files, secretFile{name: f.secretName, path: f.secretPath})
		}
		if st == "known" && len(files) > 0 {
			if stored == nil {
				stored = map[string][]byte{}
				all, _ := e.Secrets()
				for _, s := range all {
					stored[s.Name] = s.Value
				}
			}
			for _, sf := range files {
				v, ok := stored[sf.name]
				if !ok {
					if m, err := Machine(e.Home); err == nil {
						for _, n := range m.Names() {
							if x, found := stored[sf.name+"."+n]; found {
								v = x
								break
							}
						}
					}
				}
				if cur, err := os.ReadFile(sf.path); err == nil && !bytes.Equal(withoutOwn(v), withoutOwn(cur)) {
					st = "changed"
					break
				}
			}
		}
		out[f.ID] = st
	}
	return out
}

// ScopeSteps limits steps to one machine profile. Scoped steps get their
// own names ("<name>@<profile>"), so the common version of a step and the
// versions for different kinds of machines live side by side.
func ScopeSteps(steps []recipe.Step, profile string) []recipe.Step {
	out := make([]recipe.Step, len(steps))
	for i, s := range steps {
		w := map[string]string{}
		for k, v := range s.When {
			w[k] = v
		}
		w["profile"] = profile
		s.When = w
		if s.Name != "" {
			s.Name += "@" + profile
		}
		out[i] = s
	}
	return out
}

// EnsureHereProfile returns the profile of this machine, creating one from
// its facts (named by GPU model, else platform) when none matches. A new
// profile records the current workspace, if any.
func EnsureHereProfile(home string) (name string, created bool, err error) {
	m, err := Machine(home)
	if err != nil {
		return "", false, err
	}
	if len(m.Profiles) > 0 {
		return m.Profiles[0].Name, false, nil
	}
	p := mach.Suggest(m.Facts)
	if _, taken := m.File.Get(p.Name); taken {
		p.Name += "-" + strings.ToLower(m.Facts.Arch)
	}
	if ws := paths.Workspace(); ws != "" && os.Getenv("HEARTH_WORKSPACE_FROM_PROFILE") == "" {
		p.Workspace = paths.Contract(ws)
		if strings.HasPrefix(p.Workspace, "${WORKSPACE}") {
			p.Workspace = ws
		}
	}
	m.File.Set(p)
	return p.Name, true, m.File.Save(MachinesPath(home))
}

// AcceptFor records a finding for one machine profile only: its steps run
// only there, and a file secret gets its own name so each kind of machine
// keeps its own copy (e.g. a different Codex config.toml on Macs).
func (e *Engine) AcceptFor(f Finding, learned *recipe.Recipe, profile string) (string, error) {
	g := f
	if f.secretName != "" {
		g.secretName = f.secretName + "." + profile
	}
	steps := make([]recipe.Step, len(f.steps))
	copy(steps, f.steps)
	for i := range steps {
		if steps[i].Secret == f.secretName && f.secretName != "" {
			steps[i].Secret = g.secretName
		}
	}
	g.steps = ScopeSteps(steps, profile)
	g.tool = nil // tool versions are pinned for all machines (per-arch builds are handled by the installer)
	return e.Accept(g, learned)
}

// Steps returns the finding's recipe steps (for display).
func (f Finding) Steps() []recipe.Step { return f.steps }

func profileActive(home, cond string) bool {
	m, err := Machine(home)
	if err != nil || cond == "" {
		return false
	}
	for _, n := range m.Names() {
		if n == cond {
			return true
		}
	}
	return false
}

// ownMCPTableRE matches Hearth's own MCP server tables in a Codex
// config.toml ([mcp_servers.hearth] and its subtables): every machine
// writes them with its own paths, which is not a change of the user's.
var ownMCPTableRE = regexp.MustCompile(`(?m)^\[mcp_servers\.hearth(\.[^\]]*)?\]\s*$`)

// withoutOwnMCP drops those tables (up to the next table header).
func withoutOwnMCP(b []byte) []byte {
	if !bytes.Contains(b, []byte("[mcp_servers.hearth")) {
		return b
	}
	lines := strings.Split(string(b), "\n")
	var out []string
	skip := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			skip = ownMCPTableRE.MatchString(t)
		}
		if !skip {
			out = append(out, l)
		}
	}
	return []byte(strings.TrimSpace(strings.Join(out, "\n")))
}

// withoutOwn drops what Hearth writes into agent config files on every
// machine (its MCP entry, its memory hooks), so they do not make a
// recorded copy look changed.
func withoutOwn(b []byte) []byte { return withoutOwnHooks(withoutOwnMCP(b)) }

// withoutOwnHooks normalises a JSON settings file without Hearth's hooks.
func withoutOwnHooks(b []byte) []byte {
	if t := bytes.TrimSpace(b); len(t) == 0 || t[0] != '{' {
		return b
	}
	var doc map[string]any // both sides are re-encoded, so formatting does not count
	if json.Unmarshal(b, &doc) != nil {
		return b
	}
	hooks, _ := doc["hooks"].(map[string]any)
	for ev, gs := range hooks {
		groups, _ := gs.([]any)
		var keep []any
		for _, g := range groups {
			if !hearthGroup(g) {
				keep = append(keep, g)
			}
		}
		if len(keep) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = keep
		}
	}
	if hooks != nil && len(hooks) == 0 {
		delete(doc, "hooks")
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return b
	}
	return out
}
