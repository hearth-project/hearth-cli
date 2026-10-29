package client

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hearth-project/hearth-cli/internal/proxyenv"
)

// HealthReport is the small, non-secret status a machine publishes with its
// heartbeat, so another logged-in machine (and the web console) can see
// what this one still lacks before restoring it. It holds versions and
// check results only, never credentials, conversations or file contents.
type HealthReport struct {
	Status       string                 `json:"status"` // ready, needs_setup
	Client       string                 `json:"client,omitempty"`
	Machine      map[string]any         `json:"machine,omitempty"`
	Workspace    string                 `json:"workspace,omitempty"`
	Profiles     []string               `json:"profiles,omitempty"`
	Agents       map[string]AgentHealth `json:"agents,omitempty"`
	Tools        map[string]ToolHealth  `json:"tools,omitempty"`
	RecipeTodo   int                    `json:"recipe_todo,omitempty"`
	RecipeManual int                    `json:"recipe_manual,omitempty"`
	TodoSteps    []string               `json:"todo_steps,omitempty"` // the first few, for the console
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

// ToolHealth is one tool of the client setup guide (docs/*/client-env.md).
type ToolHealth struct {
	Expected  bool   `json:"expected"` // this machine's setup needs it
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"` // x.y.z
	OK        *bool  `json:"ok,omitempty"`      // its own check (codex-mixin doctor); nil = none
	Detail    string `json:"detail,omitempty"`  // why the check failed
}

var semverRE = regexp.MustCompile(`\d+\.\d+\.\d+`)

// healthTools are the tools of the setup guide that are checked: binary,
// and whether an agent being enabled makes it expected.
var healthTools = []struct{ name, bin, agent string }{
	{"node", "node", ""},
	{"codex", "codex", "codex"},
	{"claude", "claude", "claude"},
	{"codex-mixin", "codex-mixin", ""},
}

// MachineHealth runs the setup guide's checks: which tools are installed in
// which version, whether the agents' Hearth integration is on, codex-mixin's
// doctor, and the restore steps left. It never reads secret files.
func (e *Engine) MachineHealth() HealthReport {
	EnsureUserPath() // tools installed after the daemon started
	r := HealthReport{Client: Version, Agents: map[string]AgentHealth{}, Tools: map[string]ToolHealth{}, CheckedAt: time.Now().Unix()}
	if m, err := Machine(e.Home); err == nil {
		b, _ := json.Marshal(m.Facts)
		_ = json.Unmarshal(b, &r.Machine)
		r.Profiles, r.Workspace = m.Names(), m.Workspace()
	}
	enabled := EnabledAgents(e.Home)
	for _, name := range SupportedAgents {
		a := AgentHealth{Enabled: contains(enabled, name)}
		if bin, err := exec.LookPath(name); err == nil {
			a.Installed = true
			a.Version = commandVersion(bin)
			a.MCP = name == "comate" || commandOK(bin, "mcp", "get", "hearth")
			a.Ready = a.MCP
		}
		r.Agents[name] = a
	}
	mixinInFlow := false
	if plan, err := e.Plan(); err == nil {
		for _, p := range plan {
			if strings.Contains(p.Name+" "+p.Do+" "+p.Recipe, "codex-mixin") {
				mixinInFlow = true
			}
			if p.Status == "todo" {
				r.RecipeTodo++
				if p.Interactive || p.Type == "manual" {
					r.RecipeManual++
				}
				if len(r.TodoSteps) < 8 {
					n := p.Name
					if n == "" {
						n = p.Do
					}
					r.TodoSteps = append(r.TodoSteps, truncHealth(n, 80))
				}
			}
		}
	}
	for _, t := range healthTools {
		th := ToolHealth{}
		if t.agent != "" {
			th.Expected = contains(enabled, t.agent)
		}
		if bin, err := exec.LookPath(t.bin); err == nil {
			th.Installed = true
			th.Version = semverRE.FindString(commandVersion(bin))
			if t.name == "codex-mixin" {
				th.OK, th.Detail = cachedMixinDoctor(bin)
			}
		}
		if t.name == "codex-mixin" {
			th.Expected = mixinInFlow || th.Installed
		}
		if t.name == "node" {
			th.Expected = r.Tools["codex"].Expected && !r.Tools["codex"].Installed
		}
		r.Tools[t.name] = th
	}
	for _, name := range SupportedAgents {
		a := r.Agents[name]
		if a.Enabled && !a.Installed {
			r.Issues = append(r.Issues, name+" is not installed")
		} else if a.Enabled && !a.Ready {
			r.Issues = append(r.Issues, name+" is installed but its hearth integration is not ready")
		}
	}
	if m := r.Tools["codex-mixin"]; m.Expected && !m.Installed {
		r.Issues = append(r.Issues, "codex-mixin is not installed")
	} else if m.OK != nil && !*m.OK {
		r.Issues = append(r.Issues, "codex-mixin doctor: "+m.Detail)
	}
	if r.RecipeTodo > 0 {
		r.Issues = append(r.Issues, "restore flow has "+strconv.Itoa(r.RecipeTodo)+" pending step(s)")
	}
	r.Status = "ready"
	if len(r.Issues) > 0 {
		r.Status = "needs_setup"
	}
	return r
}

// FreshChecks makes the next MachineHealth run the slow checks again
// (hearth machine check) instead of reusing a recent result.
var FreshChecks bool

var mixinCache struct {
	sync.Mutex
	at      time.Time
	running bool
	ok      *bool
	detail  string
}

// cachedMixinDoctor returns the latest doctor result and refreshes it in
// the background every 15 minutes: the doctor asks the provider for its
// models, which can take half a minute, and must not hold up the sync.
// With FreshChecks it runs now and waits.
func cachedMixinDoctor(bin string) (*bool, string) {
	run := func() {
		ok, detail, known := mixinDoctor(bin)
		mixinCache.Lock()
		defer mixinCache.Unlock()
		mixinCache.at, mixinCache.running = time.Now(), false
		mixinCache.ok, mixinCache.detail = nil, ""
		if known {
			mixinCache.ok, mixinCache.detail = &ok, detail
		}
	}
	mixinCache.Lock()
	if FreshChecks {
		mixinCache.running = true
		mixinCache.Unlock()
		run()
		mixinCache.Lock()
	} else if !mixinCache.running && (mixinCache.at.IsZero() || time.Since(mixinCache.at) >= 15*time.Minute) {
		mixinCache.running = true
		go run()
	}
	defer mixinCache.Unlock()
	return mixinCache.ok, mixinCache.detail
}

// mixinDoctor runs codex-mixin's own check (gateway running, provider
// logged in and healthy). known is false when it did not finish in time.
func mixinDoctor(bin string) (ok bool, detail string, known bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "doctor", "--no-tui")
	cmd.Env = proxyenv.Env(nil) // its local gateway must not go through the proxy
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return false, "", false // slow provider: no verdict rather than a false alarm
	}
	text := string(out)
	if err == nil && strings.Contains(text, "doctor: ok") {
		return true, "", true
	}
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "✗") || strings.HasPrefix(l, "×") {
			return false, truncHealth(strings.TrimSpace(strings.TrimLeft(l, "✗× ")), 160), true
		}
	}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		low := strings.ToLower(l)
		if strings.Contains(low, "fail") || strings.Contains(low, "error") || strings.Contains(low, "not ") || strings.Contains(low, "missing") {
			return false, truncHealth(l, 160), true
		}
	}
	if err != nil {
		return false, truncHealth(err.Error(), 160), true
	}
	return false, "doctor did not report ok", true
}

func truncHealth(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func commandVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, _ := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	for _, l := range strings.Split(string(b), "\n") {
		if semverRE.MatchString(l) {
			return strings.TrimSpace(l) // skip warnings printed before the version
		}
	}
	return strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
}

func commandOK(bin string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, bin, args...).Run() == nil
}
