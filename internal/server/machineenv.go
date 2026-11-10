package server

import (
	"bufio"
	"encoding/json"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// sshReach remembers, per address, whether the server reached an SSH
// server there (banner within 3 s). A check runs in the background at most
// every 5 minutes, so listing machines never waits for it.
type sshReach struct {
	mu sync.Mutex
	m  map[string]sshReachEntry
}

type sshReachEntry struct {
	ok, running bool
	at          time.Time
}

func (r *sshReach) check(addr string, port int) (ok, done bool) {
	if addr == "" || port == 0 {
		return false, false
	}
	k := net.JoinHostPort(addr, strconv.Itoa(port))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]sshReachEntry{}
	}
	e, seen := r.m[k]
	if (!seen || time.Since(e.at) > 5*time.Minute) && !e.running {
		e.running = true
		r.m[k] = e
		go func() {
			res := sshAnswers(k)
			r.mu.Lock()
			r.m[k] = sshReachEntry{ok: res, at: time.Now()}
			r.mu.Unlock()
		}()
	}
	return e.ok, seen && !e.at.IsZero()
}

func sshAnswers(hostport string) bool {
	c, err := net.DialTimeout("tcp", hostport, 3*time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	return err == nil && strings.HasPrefix(line, "SSH-")
}

// The environment check of a machine, from its latest health report (sent
// with the heartbeat) and the latest tool releases: what it lacks compared
// with the client setup guide. Shown in the console and by `hearth machine`.

// EnvProblem is one finding. Code says what it is; the console words it.
type EnvProblem struct {
	Level  string   `json:"level"` // error, warn
	Code   string   `json:"code"`  // missing, outdated, doctor, integration, node, steps, hearth, bypass, unreachable, shared_home, no_login, stale_app_server, ssh_down, ssh_hostkey, ssh_blocked, ssh_nat
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
	SSH       *EnvSSH      `json:"ssh,omitempty"`
	CheckedAt int64        `json:"checked_at,omitempty"`
}

// EnvSSH says whether the user's devices can SSH into the machine.
type EnvSSH struct {
	Status  string `json:"status"` // ok, off, down, blocked (the machine answers itself, the server cannot reach it), checking
	Address string `json:"address,omitempty"`
	Port    int    `json:"port,omitempty"`
	Detail  string `json:"detail,omitempty"`
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
			Home     string   `json:"home"`
			SetBy    string   `json:"set_by"`
			Provider string   `json:"provider"`
			BaseURL  string   `json:"base_url"`
			Gateway  bool     `json:"gateway"`
			Reach    *bool    `json:"reach"`
			Detail   string   `json:"detail"`
			Shared   bool     `json:"shared"`
			Missing  []string `json:"missing"`
		} `json:"route"`
		StaleAppServers []struct {
			PID       int    `json:"pid"`
			CodexHome string `json:"codex_home"`
		} `json:"stale_app_servers"`
	} `json:"tools"`
	SSH *struct {
		On        bool     `json:"on"`
		Running   bool     `json:"running"`
		Address   string   `json:"address"`
		Addresses []string `json:"addresses"` // all it listens on (newer clients, when more than one)
		Port      int      `json:"port"`
		Answers   bool     `json:"answers"`
		KeyOK     bool     `json:"key_ok"`
		Detail    string   `json:"detail"`
	} `json:"ssh"`
	Client     string   `json:"client"` // Hearth build that made the report (newer than the stored one right after an update)
	RecipeTodo int      `json:"recipe_todo"`
	TodoSteps  []string `json:"todo_steps"`
	CheckedAt  int64    `json:"checked_at"`
}

var envToolOrder = []string{"codex", "codex-mixin", "claude", "node"}

// toolAgent names the agent a tool belongs to; its version and doctor are
// only judged when the user works with that agent.
var toolAgent = map[string]string{"codex": "codex", "codex-mixin": "codex", "claude": "claude"}

// machineEnv judges a machine. clientVersion is the Hearth build it runs,
// lastIP the address its requests come from, device its device id (to see
// whether it holds a relay connection).
func (s *Server) machineEnv(healthJSON, clientVersion, lastIP, device string) *MachineEnv {
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
			// Not set up yet (new machines restore software on demand): a
			// thing to do, not a fault.
			add(EnvProblem{Level: "warn", Code: "missing", Tool: name})
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
		case len(ro.Missing) > 0:
			// No login where its sessions look: Codex Desktop asks to log in.
			d := "CODEX_HOME=" + ro.Home
			if ro.SetBy != "" {
				d += ", set by " + ro.SetBy
			}
			add(EnvProblem{Level: "error", Code: "no_login", Tool: "codex", Have: strings.Join(ro.Missing, ", "), Detail: d})
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
	// Codex app-servers started before the agent home changed keep the old one.
	if st := h.Tools["codex"].StaleAppServers; len(st) > 0 {
		add(EnvProblem{Level: "warn", Code: "stale_app_server", Tool: "codex", Behind: len(st), Detail: st[0].CodexHome})
	}
	for _, name := range []string{"codex", "claude"} {
		if a, ok := h.Agents[name]; ok && a.Enabled && a.Installed && !a.Ready && (a.Used == nil || *a.Used) {
			add(EnvProblem{Level: "warn", Code: "integration", Tool: name})
		}
	}
	if h.RecipeTodo > 0 {
		add(EnvProblem{Level: "warn", Code: "steps", Behind: h.RecipeTodo, Steps: h.TodoSteps})
	}
	if x := h.SSH; x != nil {
		es := &EnvSSH{Address: x.Address, Port: x.Port, Detail: x.Detail}
		switch {
		case !x.On:
			es.Status = "off"
		case s.now()-h.CheckedAt > 15*60:
			es.Status = "stale" // an old report (machine offline): nothing to judge now
		case !x.Answers:
			es.Status = "down"
			add(EnvProblem{Level: "error", Code: "ssh_down", Tool: "ssh", Detail: x.Detail})
		case !x.KeyOK:
			es.Status = "down"
			add(EnvProblem{Level: "error", Code: "ssh_hostkey", Tool: "ssh", Detail: x.Detail})
		default:
			addrs := x.Addresses
			if len(addrs) == 0 {
				addrs = []string{x.Address}
			}
			reached, done := "", true
			for _, a := range addrs {
				ok, d := s.sshReach.check(a, x.Port)
				if ok && reached == "" {
					reached = a
				}
				done = done && d
			}
			switch {
			case reached != "":
				es.Status, es.Address = "ok", reached
			case !done:
				es.Status = "checking"
			case s.relayConnected(device):
				// The server cannot dial the sshd itself (a container's private
				// network), but the machine accepts connections through the
				// relay, which is how every device reaches it: not a problem.
				es.Status, es.Detail = "ok", "reachable through the server relay"
			default:
				es.Status = "blocked"
				p := EnvProblem{Level: "warn", Code: "ssh_blocked", Tool: "ssh", Have: x.Address + ":" + strconv.Itoa(x.Port)}
				if lastIP != "" && !slices.Contains(addrs, lastIP) {
					// It reaches the server from an address it does not
					// listen on (has not): NAT, nobody can connect in.
					p.Code, p.Detail = "ssh_nat", lastIP
					es.Detail = "behind NAT: its requests come from " + lastIP
				}
				add(p)
			}
		}
		env.SSH = es
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
