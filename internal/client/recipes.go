package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/recipe"
	"github.com/hearth-project/hearth-cli/internal/tools"
)

// RecipesDir holds library/recipes/*.toml (synced with the library).
func RecipesDir(home string) string { return filepath.Join(LibraryDir(home), "recipes") }

// recipeHost adapts the engine to recipe.Host, fetching secrets once.
type recipeHost struct {
	e       *Engine
	secrets map[string][]byte
}

func (h *recipeHost) load() error {
	if h.secrets != nil {
		return nil
	}
	all, err := h.e.Secrets()
	if err != nil {
		return err
	}
	h.secrets = map[string][]byte{}
	for _, s := range all {
		h.secrets[s.Name] = s.Value
	}
	return nil
}

func (h *recipeHost) Secret(name string) ([]byte, bool, error) {
	if err := h.load(); err != nil {
		return nil, false, err
	}
	v, ok := h.secrets[name]
	return v, ok, nil
}

func (h *recipeHost) SetSecret(name string, v []byte) error {
	if err := h.e.SetSecret(Secret{Name: name, Value: v}); err != nil {
		return err
	}
	if h.secrets != nil {
		h.secrets[name] = v
	}
	return nil
}

func (h *recipeHost) LibraryFile(rel string) ([]byte, error) {
	// The user's library first, then the site defaults.
	var firstErr error
	for _, root := range []string{LibraryDir(h.e.Home), SiteDir(h.e.Home)} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if !strings.HasPrefix(p, root+string(filepath.Separator)) {
			return nil, fmt.Errorf("library path %q escapes the library", rel)
		}
		b, err := os.ReadFile(p)
		if err == nil {
			return b, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// LoadRecipes reads the library's recipes; only selects them when names given.
func LoadRecipes(home string, only []string) ([]*recipe.Recipe, error) {
	_, _ = LoadLearned(home) // migrates steps of older versions
	all, err := recipe.LoadDir(RecipesDir(home))
	if err != nil {
		return nil, err
	}
	// Site defaults from the server come first; the user's own recipe of
	// the same name replaces the site's.
	site, err := recipe.LoadDir(filepath.Join(SiteDir(home), "recipes"))
	if err != nil {
		return nil, fmt.Errorf("site defaults: %w", err)
	}
	if len(site) > 0 {
		mine := map[string]bool{}
		for _, r := range all {
			mine[r.Name] = true
		}
		for _, n := range SiteDisabled(home) {
			mine[n] = true // the user opted out of this site recipe
		}
		var merged []*recipe.Recipe
		covered := map[string]bool{} // secrets a site recipe captures
		for _, r := range site {
			if !mine[r.Name] {
				merged = append(merged, r)
				for _, s := range r.Steps {
					if s.Type == "capture" && s.Secret != "" {
						covered[s.Secret] = true
					}
				}
			}
		}
		// A learned copy of what a site recipe already does (codex-mixin
		// learned before the site recipe existed) would ask twice.
		for _, r := range all {
			if r.Name == LearnedRecipe && covered["codex-mixin-config"] {
				var keep []recipe.Step
				for _, s := range r.Steps {
					if !strings.HasPrefix(s.Name, "codex-mixin-") {
						keep = append(keep, s)
					}
				}
				r.Steps = keep
			}
		}
		all = append(merged, all...)
	}
	if len(only) == 0 {
		return all, nil
	}
	want := map[string]bool{}
	for _, n := range only {
		want[n] = true
	}
	var out []*recipe.Recipe
	for _, r := range all {
		if want[r.Name] {
			out = append(out, r)
			delete(want, r.Name)
		}
	}
	for n := range want {
		return nil, fmt.Errorf("no recipe named %q", n)
	}
	return out, nil
}

// RunRecipes executes recipes with the engine's secrets and library.
func (e *Engine) RunRecipes(only []string, opt recipe.Options) ([]recipe.Result, error) {
	rs, err := LoadRecipes(e.Home, only)
	if err != nil {
		return nil, err
	}
	if t := e.toolsRecipe(rs); t != nil && len(only) == 0 {
		rs = append([]*recipe.Recipe{t}, rs...)
	}
	opt.StateDir = e.Home
	share := ShareApprovals(e.Home)
	if share && opt.Approved == nil {
		opt.Approved = e.sharedApprovals()
	}
	if opt.Machine == nil {
		m, err := Machine(e.Home)
		if err != nil {
			return nil, err
		}
		opt.Machine, opt.Profiles = &m.Facts, m.Names()
	}
	res, err := recipe.Run(rs, &recipeHost{e: e}, opt)
	if share && !opt.DryRun {
		e.publishApprovals(opt.Approved)
	}
	return res, err
}

// approvalsSecret holds the recipe commands the user approved on any
// machine. It is a vault secret, so the server cannot add approvals.
const approvalsSecret = "hearth.recipe-approvals"

func (e *Engine) sharedApprovals() map[string]bool {
	out := map[string]bool{}
	s, err := e.Secret(approvalsSecret)
	if err != nil {
		return out
	}
	var list []string
	if json.Unmarshal(s.Value, &list) == nil {
		for _, k := range list {
			out[k] = true
		}
	}
	return out
}

// publishApprovals uploads this machine's approvals when the shared set
// lacks some of them (best effort: offline is fine).
func (e *Engine) publishApprovals(known map[string]bool) {
	local := recipe.LoadApprovals(e.Home)
	all := map[string]bool{}
	for k := range known {
		all[k] = true
	}
	added := false
	for k := range local {
		if !all[k] {
			all[k], added = true, true
		}
	}
	if !added {
		return
	}
	list := make([]string, 0, len(all))
	for k := range all {
		list = append(list, k)
	}
	sort.Strings(list)
	b, _ := json.Marshal(list)
	_ = e.SetSecret(Secret{Name: approvalsSecret, Value: b})
}

// ShareApprovals reports whether a recipe command approved on one machine
// runs on the user's other machines without asking again (config.toml
// [recipes] share_approvals, default on).
func ShareApprovals(home string) bool {
	c, err := UserConfig(home)
	return err != nil || c.Recipes == nil || c.Recipes.ShareApprovals == nil || *c.Recipes.ShareApprovals
}

// SiteCaptures reports whether an enabled site recipe keeps this secret
// itself (then learn does not record its own steps for it).
func SiteCaptures(home, secret string) bool {
	site, _ := recipe.LoadDir(filepath.Join(SiteDir(home), "recipes"))
	off := map[string]bool{}
	for _, n := range SiteDisabled(home) {
		off[n] = true
	}
	for _, r := range site {
		if off[r.Name] {
			continue
		}
		for _, s := range r.Steps {
			if s.Type == "capture" && s.Secret == secret {
				return true
			}
		}
	}
	return false
}

// Plan lists the steps of the user's recipes (site defaults included) that
// install software or run commands on this machine: the hearth skill works
// through them with the user; Hearth itself does not run them on setup.
func (e *Engine) Plan() ([]recipe.PlanStep, error) {
	rs, err := LoadRecipes(e.Home, nil)
	if err != nil {
		return nil, err
	}
	m, err := Machine(e.Home)
	if err != nil {
		return nil, err
	}
	if t := e.toolsRecipe(rs); t != nil {
		rs = append([]*recipe.Recipe{t}, rs...)
	}
	h := &recipeHost{e: e}
	return recipe.Plan(rs, recipe.PlanOptions{StateDir: e.Home, Machine: &m.Facts, Profiles: m.Names(),
		Secret: func(name string) (bool, error) { _, ok, err := h.Secret(name); return ok, err }}), nil
}

// MarkPlanDone records plan steps as done on this machine.
func (e *Engine) MarkPlanDone(keys ...string) error { return recipe.MarkDone(e.Home, keys...) }

// toolsRecipe turns config.toml [tools] (pinned agent CLIs, custom tools)
// into plan steps, for the agents this machine restores, unless a recipe
// installs the tool already.
func (e *Engine) toolsRecipe(rs []*recipe.Recipe) *recipe.Recipe {
	m, err := LoadTools(e.Home)
	if err != nil || len(m.Tools) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, r := range rs {
		for _, s := range r.Steps {
			have[s.Name] = true // a recipe's own step for this tool (any type)
		}
	}
	here := map[string]bool{}
	for _, a := range MachineAgents(e.Home) {
		here[a] = true
	}
	agent := map[string]bool{}
	for _, a := range SupportedAgents {
		agent[a] = true
	}
	out := &recipe.Recipe{Name: "tools", Description: "config.toml [tools]"}
	for _, t := range m.Tools {
		if have[t.Name] || (agent[t.Name] && !here[t.Name]) {
			continue
		}
		if b, ok := tools.Builtins[t.Name]; ok && t.Install == "" {
			pkg := b.NPM
			if t.Version != "" {
				pkg += "@" + t.Version
			}
			out.Steps = append(out.Steps, recipe.Step{Type: "tool", Name: t.Name, NPM: pkg, Version: t.Version, Check: b.Binary + " --version"})
			continue
		}
		if t.Install != "" {
			out.Steps = append(out.Steps, recipe.Step{Type: "run", Name: t.Name,
				Run: strings.ReplaceAll(t.Install, "${VERSION}", t.Version), Unless: t.Check})
		}
	}
	if len(out.Steps) == 0 {
		return nil
	}
	return out
}
