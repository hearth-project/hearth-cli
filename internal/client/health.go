package client

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// HealthReport is the small, non-secret status a machine publishes. It is
// intentionally suitable for another machine to inspect before starting a
// remote restore flow.
type HealthReport struct {
	Status       string                 `json:"status"` // ready, needs_setup, degraded
	Client       string                 `json:"client,omitempty"`
	Machine      map[string]any         `json:"machine,omitempty"`
	Workspace    string                 `json:"workspace,omitempty"`
	Profiles     []string               `json:"profiles,omitempty"`
	Agents       map[string]AgentHealth `json:"agents,omitempty"`
	RecipeTodo   int                    `json:"recipe_todo,omitempty"`
	RecipeManual int                    `json:"recipe_manual,omitempty"`
	Issues       []string               `json:"issues,omitempty"`
	CheckedAt    int64                  `json:"checked_at"`
}

type AgentHealth struct {
	Enabled   bool   `json:"enabled"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	MCP       bool   `json:"mcp,omitempty"`
	Ready     bool   `json:"ready"`
}

// MachineHealth computes an agent-facing readiness report. It only runs
// version/help checks and reads recipe state; it never reads secret files.
func (e *Engine) MachineHealth() HealthReport {
	r := HealthReport{Client: Version, Agents: map[string]AgentHealth{}, CheckedAt: time.Now().Unix()}
	if m, err := Machine(e.Home); err == nil {
		b, _ := json.Marshal(m.Facts)
		_ = json.Unmarshal(b, &r.Machine)
		r.Profiles, r.Workspace = m.Names(), m.Workspace()
	}
	for _, name := range SupportedAgents {
		a := AgentHealth{Enabled: contains(EnabledAgents(e.Home), name)}
		if bin, err := exec.LookPath(name); err == nil {
			a.Installed = true
			a.Version = commandVersion(bin)
			a.MCP = name == "comate" || commandOK(bin, "mcp", "get", "hearth")
			a.Ready = a.MCP || name == "comate"
		}
		r.Agents[name] = a
	}
	if plan, err := e.Plan(); err == nil {
		for _, p := range plan {
			if p.Status == "todo" {
				r.RecipeTodo++
				if p.Interactive || p.Type == "manual" {
					r.RecipeManual++
				}
			}
		}
	}
	for name, a := range r.Agents {
		if a.Enabled && !a.Installed {
			r.Issues = append(r.Issues, name+" is not installed")
		} else if a.Enabled && !a.Ready {
			r.Issues = append(r.Issues, name+" is installed but its hearth integration is not ready")
		}
	}
	if r.RecipeTodo > 0 {
		r.Issues = append(r.Issues, "restore flow has "+healthItoa(r.RecipeTodo)+" pending step(s)")
	}
	if len(r.Issues) == 0 {
		r.Status = "ready"
	} else if len(r.Agents) > 0 {
		r.Status = "needs_setup"
	}
	return r
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
func healthItoa(n int) string { return strconv.Itoa(n) }

func commandVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, _ := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	return strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
}
func commandOK(bin string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, bin, args...).Run() == nil
}
