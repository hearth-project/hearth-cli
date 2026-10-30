package server

import (
	"encoding/json"
	"strings"
)

// The environment check of a machine, from its latest health report (sent
// with the heartbeat) and the latest tool releases: what it lacks compared
// with the client setup guide. Shown in the console and by `hearth machine`.

// EnvProblem is one finding. Code says what it is; the console words it.
type EnvProblem struct {
	Level  string   `json:"level"` // error, warn
	Code   string   `json:"code"`  // missing, outdated, doctor, integration, node, steps, hearth
	Tool   string   `json:"tool,omitempty"`
	Have   string   `json:"have,omitempty"`
	Latest string   `json:"latest,omitempty"`
	Behind int      `json:"behind,omitempty"`
	More   bool     `json:"more,omitempty"` // at least Behind
	Detail string   `json:"detail,omitempty"`
	Steps  []string `json:"steps,omitempty"`
}

// MachineEnv is a machine's environment check.
type MachineEnv struct {
	Status    string       `json:"status"` // ok, warn, error, unknown
	Problems  []EnvProblem `json:"problems"`
	Tools     []EnvTool    `json:"tools,omitempty"`
	CheckedAt int64        `json:"checked_at,omitempty"`
}

// EnvTool is one tool row of the check.
type EnvTool struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Expected  bool   `json:"expected"`
}

type reportedHealth struct {
	Agents map[string]struct {
		Enabled, Installed, Ready bool
		Used                      *bool // nil: client older than usage-based checks
		Version                   string
	} `json:"agents"`
	Tools map[string]struct {
		Expected, Installed bool
		Version             string
		OK                  *bool
		Detail              string
		Route               *struct {
			Home     string `json:"home"`
			SetBy    string `json:"set_by"`
			Provider string `json:"provider"`
			BaseURL  string `json:"base_url"`
			Gateway  bool   `json:"gateway"`
			Reach    *bool  `json:"reach"`
			Detail   string `json:"detail"`
			Shared   bool   `json:"shared"`
		} `json:"route"`
	} `json:"tools"`
	Client     string   `json:"client"` // Hearth build that made the report (newer than the stored one right after an update)
	RecipeTodo int      `json:"recipe_todo"`
	TodoSteps  []string `json:"todo_steps"`
	CheckedAt  int64    `json:"checked_at"`
}

var envToolOrder = []string{"codex", "codex-mixin", "claude", "node"}

// toolAgent names the agent a tool belongs to; its version and doctor are
// only judged when the user works with that agent.
var toolAgent = map[string]string{"codex": "codex", "codex-mixin": "codex", "claude": "claude"}

// machineEnv judges a machine. clientVersion is the Hearth build it runs.
func (s *Server) machineEnv(healthJSON, clientVersion string) *MachineEnv {
	var h reportedHealth
	if strings.TrimSpace(healthJSON) == "" || healthJSON == "{}" || json.Unmarshal([]byte(healthJSON), &h) != nil || h.CheckedAt == 0 {
		return &MachineEnv{Status: "unknown", Problems: []EnvProblem{}}
	}
	env := &MachineEnv{Problems: []EnvProblem{}, CheckedAt: h.CheckedAt}
	add := func(p EnvProblem) { env.Problems = append(env.Problems, p) }
	unused := func(tool string) bool {
		a, ok := h.Agents[toolAgent[tool]]
		return ok && a.Used != nil && !*a.Used
	}
	for _, name := range envToolOrder {
		t, ok := h.Tools[name]
		if !ok {
			continue
		}
		row := EnvTool{Name: name, Installed: t.Installed, Version: t.Version, Expected: t.Expected}
		releases := s.versions.get(name)
		if len(releases) > 0 {
			row.Latest = releases[0]
		}
		if t.Expected || (t.Installed && !unused(name)) {
			env.Tools = append(env.Tools, row)
		}
		switch {
		case name == "node":
			if t.Expected && (!t.Installed || cmpVersion(t.Version, "22.0.0") < 0) {
				add(EnvProblem{Level: "warn", Code: "node", Tool: name, Have: t.Version})
			}
		case t.Expected && !t.Installed:
			add(EnvProblem{Level: "error", Code: "missing", Tool: name})
		case t.Installed && !unused(name):
			if latest, n, more := behind(releases, t.Version); n > 0 {
				add(EnvProblem{Level: "warn", Code: "outdated", Tool: name, Have: t.Version, Latest: latest, Behind: n, More: more})
			}
			if t.OK != nil && !*t.OK {
				add(EnvProblem{Level: "error", Code: "doctor", Tool: name, Detail: t.Detail})
			}
		}
	}
	// Codex sessions that do not reach their model: another config wins
	// (a colleague's CODEX_HOME on a shared account), or its endpoint is
	// unreachable from here.
	if c, ok := h.Tools["codex"]; ok && c.Route != nil {
		ro := c.Route
		mixin := h.Tools["codex-mixin"]
		switch {
		case mixin.Installed && !ro.Gateway:
			d := "CODEX_HOME=" + ro.Home + " (" + ro.Provider + ")"
			if ro.SetBy != "" {
				d += ", set by " + ro.SetBy
			}
			add(EnvProblem{Level: "error", Code: "bypass", Tool: "codex", Detail: d})
		case ro.Reach != nil && !*ro.Reach:
			add(EnvProblem{Level: "error", Code: "unreachable", Tool: "codex", Have: ro.BaseURL, Detail: ro.Detail})
		}
		// The gateway login sits in the account-wide default: everybody on
		// this account whose shell does not set CODEX_HOME uses it.
		if ro.Shared && ro.Gateway {
			add(EnvProblem{Level: "warn", Code: "shared_home", Tool: "codex", Detail: ro.Home})
		}
	}
	for _, name := range []string{"codex", "claude"} {
		if a, ok := h.Agents[name]; ok && a.Enabled && a.Installed && !a.Ready && (a.Used == nil || *a.Used) {
			add(EnvProblem{Level: "warn", Code: "integration", Tool: name})
		}
	}
	if h.RecipeTodo > 0 {
		add(EnvProblem{Level: "warn", Code: "steps", Behind: h.RecipeTodo, Steps: h.TodoSteps})
	}
	if h.Client != "" {
		clientVersion = h.Client
	}
	if served := s.servedVersion(); served != "" && clientVersion != "" && clientVersion < served && !strings.HasSuffix(clientVersion, "-dev") {
		add(EnvProblem{Level: "warn", Code: "hearth", Tool: "hearth", Have: clientVersion, Latest: served})
	}
	env.Status = "ok"
	for _, p := range env.Problems {
		if p.Level == "error" {
			env.Status = "error"
			break
		}
		env.Status = "warn"
	}
	return env
}
