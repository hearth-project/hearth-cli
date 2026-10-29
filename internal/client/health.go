package client

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
	Enabled bool `json:"enabled"`
	// Used: the user works with this agent (a conversation on any of their
	// machines within RecentWindow of their latest one); only used agents
	// are expected to be installed, so an agent tried once is not nagged.
	Used      bool   `json:"used"`
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
	Path      string `json:"path,omitempty"`    // where it was found
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
	used := enabled
	if all, err := e.List(); err == nil {
		used, _ = RecentAgents(e.Home, AgentUse(all))
	}
	for _, name := range SupportedAgents {
		a := AgentHealth{Enabled: contains(enabled, name), Used: contains(used, name)}
		if bin, err := lookTool(name); err == nil {
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
			th.Expected = r.Agents[t.agent].Enabled && r.Agents[t.agent].Used
		}
		if bin, err := lookTool(t.bin); err == nil {
			th.Installed, th.Path = true, bin
			th.Version = semverRE.FindString(commandVersion(bin))
			if t.name == "codex-mixin" {
				th.OK, th.Detail = cachedMixinDoctor(bin)
			}
		}
		if t.name == "codex-mixin" {
			th.Expected = (mixinInFlow && r.Agents["codex"].Used) || th.Installed // a Codex gateway
		}
		if t.name == "node" {
			th.Expected = r.Tools["codex"].Expected && !r.Tools["codex"].Installed
		}
		r.Tools[t.name] = th
	}
	for _, name := range SupportedAgents {
		a := r.Agents[name]
		if !a.Used {
			continue // not one of the user's agents (enabled, but not used lately)
		}
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

// lookTool finds a tool the way the user reaches it: on PATH, else inside
// an app bundle (the Codex app ships its CLI), else as a running process
// (a gateway started by its own service), else in a launchd agent (macOS).
func lookTool(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err == nil {
		return p, nil
	}
	if name == "codex" {
		if c := appBundleBinary("Codex", name); c != "" {
			return c, nil
		}
	}
	if c := runningBinary(name); c != "" {
		return c, nil
	}
	if c := launchAgentBinary(name); c != "" {
		return c, nil
	}
	return "", err
}

func isExecFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

// appBundleBinary looks for an executable called name in *<app>*.app
// bundles under /Applications and ~/Applications (a few levels deep).
func appBundleBinary(app, name string) string {
	h, _ := os.UserHomeDir()
	for _, dir := range []string{"/Applications", filepath.Join(h, "Applications")} {
		apps, _ := filepath.Glob(filepath.Join(dir, "*"+app+"*.app"))
		for _, a := range apps {
			root := filepath.Join(a, "Contents")
			found := ""
			_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
				if err != nil || found != "" {
					return filepath.SkipDir
				}
				if d.IsDir() {
					if strings.Count(strings.TrimPrefix(p, root), string(filepath.Separator)) > 5 || strings.HasSuffix(p, ".framework") || strings.HasSuffix(p, ".lproj") {
						return filepath.SkipDir
					}
					return nil
				}
				if d.Name() == name && isExecFile(p) {
					found = p
					return filepath.SkipAll
				}
				return nil
			})
			if found != "" {
				return found
			}
		}
	}
	return ""
}

// runningBinary is the executable of a running process called name.
func runningBinary(name string) string {
	if runtime.GOOS == "linux" {
		exes, _ := filepath.Glob("/proc/[0-9]*/exe")
		for _, e := range exes {
			if p, err := os.Readlink(e); err == nil && filepath.Base(p) == name && isExecFile(p) {
				return p
			}
		}
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axww", "-o", "comm=").Output() // macOS: the full path
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); filepath.IsAbs(l) && filepath.Base(l) == name && isExecFile(l) {
			return l
		}
	}
	return ""
}

var plistStringRE = regexp.MustCompile(`<string>([^<]+)</string>`)

// launchAgentBinary finds name in the user's launchd agents (a service
// installed by the tool itself, macOS).
func launchAgentBinary(name string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	h, _ := os.UserHomeDir()
	plists, _ := filepath.Glob(filepath.Join(h, "Library", "LaunchAgents", "*.plist"))
	for _, p := range plists {
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), name) {
			continue
		}
		for _, m := range plistStringRE.FindAllStringSubmatch(string(b), -1) {
			if v := strings.TrimSpace(m[1]); filepath.IsAbs(v) && filepath.Base(v) == name && isExecFile(v) {
				return v
			}
		}
	}
	return ""
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
