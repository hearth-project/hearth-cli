package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/machine"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// The restore plan: the steps of the user's recipes that install software
// or run commands on this machine. Hearth does not run them by itself; the
// coding agent (hearth skill) works through the plan with the user, checks
// each step and marks it done here (MarkDone), and improves the recipes
// when this machine needed something else.

// PlanStep is one step of the plan.
type PlanStep struct {
	Key         string   `json:"key"` // for MarkDone / `hearth recipe done`
	Recipe      string   `json:"recipe"`
	Name        string   `json:"name,omitempty"`
	Type        string   `json:"type"` // tool, run, service, check, manual, tool-pin
	Do          string   `json:"do"`   // what to do, in words or as a command
	Commands    []string `json:"commands,omitempty"`
	Check       string   `json:"check,omitempty"` // succeeds when the step is done
	Expect      string   `json:"expect,omitempty"`
	Interactive bool     `json:"interactive,omitempty"` // the user runs it in their own terminal
	NeedsSecret string   `json:"stdin_secret,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Status      string   `json:"status"` // todo, done (marked here), skipped
	Why         string   `json:"why,omitempty"`
}

// StepKey identifies a step for MarkDone (manual steps keep ManualKey).
func StepKey(recipe string, s Step) string {
	if s.Type == "manual" {
		return ManualKey(recipe, s)
	}
	h := sha256.Sum256([]byte(s.fingerprint() + s.Text))
	return recipe + "/" + hex.EncodeToString(h[:])[:10]
}

// PlanOptions selects the machine the plan is for.
type PlanOptions struct {
	StateDir string
	Machine  *machine.Facts
	Profiles []string
	Secret   func(name string) (bool, error) // does the account have this secret
}

// Plan lists the command steps for this machine in the order the recipes
// write them (a recipe is a flow: install, configure, check), recipe by
// recipe, without running anything. Files, env and capture steps are not
// part of it (setup restores files and env, the daemon runs captures).
func Plan(recipes []*Recipe, opt PlanOptions) []PlanStep {
	if opt.Machine == nil {
		opt.Machine = &machine.Facts{OS: runtime.GOOS, Arch: Arch()}
	}
	r := &runner{opt: Options{Machine: opt.Machine, Profiles: opt.Profiles}}
	done := loadDone(opt.StateDir)
	recipes = withRecipeWhen(recipes)
	var out []PlanStep
	planned := map[string]bool{"tool": true, "run": true, "service": true, "check": true, "manual": true}
	for _, rc := range recipes {
		for _, s := range rc.Steps {
			if !planned[s.Type] {
				continue
			}
			it := item{recipe: rc.Name, step: s}
			p := PlanStep{Key: StepKey(it.recipe, s), Recipe: it.recipe, Name: s.Name, Type: s.Type, Status: "todo",
				DependsOn: s.DependsOn, Interactive: s.TTY, NeedsSecret: s.StdinSecret}
			switch s.Type {
			case "tool":
				p.Do = s.describeForAgent()
				p.Check = s.checkCmd()
				if s.Version != "" {
					p.Expect = s.Version
				}
			case "run":
				p.Do, p.Commands, p.Check = "run", []string{s.Run}, s.Unless
			case "service":
				p.Do, p.Commands, p.Check = "start in the background (it must survive the terminal, e.g. setsid nohup)", []string{s.Start}, s.Health
			case "check":
				p.Do, p.Check, p.Expect = "verify", s.Run, s.Expect
			case "manual":
				p.Do, p.Check = s.Text, s.DoneWhen
			}
			if ok, why := r.machineMatches(s.When); !ok {
				p.Status, p.Why = "skipped", why
			} else if why := secretCond(s, opt.Secret); why != "" {
				p.Status, p.Why = "skipped", why
			} else if s.Type == "check" && s.Optional {
				p.Why = "optional"
			}
			if done[p.Key] && p.Status == "todo" {
				p.Status = "done"
			}
			// Commands run in the agent's shell, which does not know ${WORKSPACE}.
			if ws := paths.Workspace(); ws != "" {
				x := func(v string) string { return strings.ReplaceAll(v, "${WORKSPACE}", ws) }
				p.Do, p.Check = x(p.Do), x(p.Check)
				for i := range p.Commands {
					p.Commands[i] = x(p.Commands[i])
				}
			}
			out = append(out, p)
		}
	}
	return out
}

func secretCond(s Step, has func(string) (bool, error)) string {
	if has == nil {
		return ""
	}
	if s.WhenSecret != "" {
		if ok, err := has(s.WhenSecret); err == nil && !ok {
			return "only once the secret " + s.WhenSecret + " exists"
		}
	}
	if s.UnlessSecret != "" {
		if ok, err := has(s.UnlessSecret); err == nil && ok {
			return "not needed: the secret " + s.UnlessSecret + " exists"
		}
	}
	return ""
}

// describeForAgent says how to install a tool step by hand.
func (s Step) describeForAgent() string {
	switch {
	case s.URL != "":
		var b strings.Builder
		b.WriteString("download " + s.expand(s.URL))
		if sum := s.SHA256[Arch()]; sum != "" {
			b.WriteString(", check sha256 " + sum)
		}
		if s.InstallDir != "" {
			b.WriteString(", unpack into " + s.InstallDir)
			if s.Strip > 0 {
				b.WriteString(" (strip the top directory)")
			}
		}
		if s.Bin != "" {
			b.WriteString(", install " + s.Bin + " as " + s.BinDest)
		} else if s.BinDest != "" {
			b.WriteString(", install it as " + s.BinDest)
		}
		return b.String()
	case s.NPM != "":
		d := "npm install -g " + s.NPM
		if s.Registry != "" {
			d += " --registry=" + s.Registry
		}
		if s.Node != "" {
			d += " (with the Node of step " + s.Node + ")"
		}
		if s.Wrapper != "" {
			d += ", then a wrapper script " + s.Wrapper + " that runs it with that Node"
		}
		return d
	}
	return "install " + s.Name
}

// MarkDone records steps as done on this machine.
func MarkDone(stateDir string, keys ...string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	d := loadDone(stateDir)
	for _, k := range keys {
		d[k] = true
	}
	return saveDone(stateDir, d)
}
