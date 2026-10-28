package client

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/hearth-project/hearth-cli/internal/crypto"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// Proposals let an agent record a setup step it just performed with the
// user (through the MCP tool propose_setup_step). They are synced with the
// library but never run until the user accepts them.

func proposalsDir(home string) string { return filepath.Join(RecipesDir(home), "proposals") }

// Proposal is one pending suggestion.
type Proposal struct {
	ID     string
	Recipe string
	Reason string
	Steps  []recipe.Step
}

// Propose validates step TOML (one or more [[step]] tables) for a recipe.
func Propose(home, recipeName, stepsTOML, reason string) (string, error) {
	if recipeName == "" {
		recipeName = LearnedRecipe
	}
	src := fmt.Sprintf("name = %q\ndescription = %q\n\n%s\n", recipeName, reason, stepsTOML)
	r, err := recipe.Parse([]byte(src))
	if err != nil {
		return "", fmt.Errorf("invalid step: %w", err)
	}
	if len(r.Steps) == 0 {
		return "", fmt.Errorf("no [[step]] table found")
	}
	id := fmt.Sprintf("%d-%x", time.Now().Unix(), crypto.RandomBytes(3))
	if err := os.MkdirAll(proposalsDir(home), 0o755); err != nil {
		return "", err
	}
	return id, os.WriteFile(filepath.Join(proposalsDir(home), id+".toml"), []byte(src), 0o644)
}

// Proposals lists pending proposals, oldest first.
func Proposals(home string) ([]Proposal, error) {
	entries, err := os.ReadDir(proposalsDir(home))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Proposal
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(proposalsDir(home), e.Name()))
		if err != nil {
			return nil, err
		}
		r, err := recipe.Parse(b)
		if err != nil {
			continue
		}
		out = append(out, Proposal{ID: strings.TrimSuffix(e.Name(), ".toml"), Recipe: r.Name, Reason: r.Description, Steps: r.Steps})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// StepTOML renders steps for display.
func StepTOML(steps []recipe.Step) string {
	b, _ := toml.Marshal(struct {
		Steps []recipe.Step `toml:"step"`
	}{steps})
	return string(b)
}

// ResolveProposal accepts (merging into its recipe) or rejects a proposal.
func ResolveProposal(home, id string, accept bool) (string, error) {
	ps, err := Proposals(home)
	if err != nil {
		return "", err
	}
	for _, p := range ps {
		if p.ID != id {
			continue
		}
		if accept {
			r, err := loadRecipeOrNew(home, p.Recipe)
			if err != nil {
				return "", err
			}
			MergeSteps(r, p.Steps)
			if err := SaveRecipe(home, r); err != nil {
				return "", err
			}
		}
		return p.Recipe, os.Remove(filepath.Join(proposalsDir(home), id+".toml"))
	}
	return "", fmt.Errorf("no proposal %q", id)
}

func loadRecipeOrNew(home, name string) (*recipe.Recipe, error) {
	if name == LearnedRecipe {
		return LoadLearned(home)
	}
	b, err := os.ReadFile(filepath.Join(RecipesDir(home), name+".toml"))
	if os.IsNotExist(err) {
		return &recipe.Recipe{Name: name}, nil
	}
	if err != nil {
		return nil, err
	}
	return recipe.Parse(b)
}

// RecipesSummary describes the current recipes for an agent (no secrets:
// recipes only reference secret names).
func RecipesSummary(home string) string {
	rs, err := recipe.LoadDir(RecipesDir(home))
	if err != nil {
		return "error: " + err.Error()
	}
	if len(rs) == 0 {
		return "No recipes yet."
	}
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "## %s — %s\n%s\n", r.Name, r.Description, StepTOML(r.Steps))
	}
	if m, err := Machine(home); err == nil {
		fmt.Fprintf(&b, "\nThis machine: %s; active profiles: %v.\n", m.Facts.Summary(), m.Names())
		for _, p := range m.File.Profiles {
			fmt.Fprintf(&b, "- profile %s: match %v workspace %q\n", p.Name, p.Match, p.Workspace)
		}
		b.WriteString("Steps without `when` run on every machine (habits); `when = { profile = \"x\" }` limits them.\n")
	}
	ps, _ := Proposals(home)
	if len(ps) > 0 {
		fmt.Fprintf(&b, "(%d proposal(s) pending user review)\n", len(ps))
	}
	return b.String()
}
