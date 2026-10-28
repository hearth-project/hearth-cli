package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/paths"
	"github.com/hearth-project/hearth-cli/skills"
)

// The onboard commands let an agent (the hearth skill) drive the
// setup of a machine: one JSON status with the recommended next steps, and
// the installation of the skill itself.

type agentState struct {
	Installed     bool   `json:"installed"`
	Enabled       bool   `json:"enabled"`
	MCPRegistered bool   `json:"mcp_registered"`
	SkillPath     string `json:"skill_path,omitempty"` // the hearth skill installed here
}

type nextStep struct {
	ID           string `json:"id"`
	Why          string `json:"why"`
	Command      string `json:"command,omitempty"`
	UserTerminal bool   `json:"user_terminal,omitempty"` // needs the master password: the user runs it
	AskUser      string `json:"ask_user,omitempty"`      // what to ask the user first
}

type onboardStatus struct {
	Version         string                `json:"version"`
	Binary          string                `json:"binary"`
	Home            string                `json:"home"`
	Workspace       string                `json:"workspace,omitempty"`
	WorkspaceSource string                `json:"workspace_source"` // init, profile, env, none
	SharedHome      bool                  `json:"shared_home_hint,omitempty"`
	LoggedIn        bool                  `json:"logged_in"`
	Server          string                `json:"server,omitempty"`
	ServerReachable *bool                 `json:"server_reachable,omitempty"`
	Registration    string                `json:"registration,omitempty"` // invite, open, closed: how new accounts sign up
	Email           string                `json:"email,omitempty"`
	Device          string                `json:"device,omitempty"`
	Machine         string                `json:"machine"`
	Profiles        []string              `json:"profiles"`
	Agents          map[string]agentState `json:"agents"`
	DaemonRunning   bool                  `json:"daemon_running"`
	Sessions        *int                  `json:"sessions_on_server,omitempty"`
	Teams           []api.Team            `json:"teams,omitempty"` // teams the user is in or can read
	LearnNew        *int                  `json:"learn_new_or_changed,omitempty"`
	RecipeTodo      int                   `json:"recipe_steps_todo"`
	RecipeManual    int                   `json:"recipe_manual_pending"`
	Next            []nextStep            `json:"next"`
}

// skillTargets are the skill directories of the agents on this machine:
// every agent with a skills directory in the library targets whose home
// exists (or whose CLI is installed).
func skillTargets() map[string]string {
	out := map[string]string{}
	for _, t := range library.DefaultTargets() {
		if _, err := os.Stat(t.Root); err != nil {
			if _, err := exec.LookPath(t.Name); err != nil {
				continue // agent not on this machine
			}
		}
		out[t.Name] = filepath.Join(t.Root, "skills")
	}
	return out
}

func mcpRegistered(agent string) bool {
	bin, err := exec.LookPath(agent)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, bin, "mcp", "get", "hearth").Run() == nil
}

func collectOnboard() onboardStatus {
	home := client.Home()
	exe, _ := os.Executable()
	st := onboardStatus{Version: version, Binary: exe, Home: home, Agents: map[string]agentState{}, Next: []nextStep{}}
	switch {
	case client.WorkspaceOf(home) != "":
		st.Workspace, st.WorkspaceSource = client.WorkspaceOf(home), "init"
	case os.Getenv("HEARTH_WORKSPACE_FROM_PROFILE") != "":
		st.Workspace, st.WorkspaceSource = paths.Workspace(), "profile"
	case paths.Workspace() != "":
		st.Workspace, st.WorkspaceSource = paths.Workspace(), "env"
	default:
		st.WorkspaceSource = "none"
	}
	if h, _ := os.UserHomeDir(); h == "/root" && st.Workspace == "" {
		st.SharedHome = true // GPU machines often share root between people
	}
	m, _ := client.Machine(home)
	st.Machine, st.Profiles = m.Facts.Summary(), m.Names()
	if st.Profiles == nil {
		st.Profiles = []string{}
	}
	enabled := map[string]bool{}
	for _, a := range client.EnabledAgents(home) {
		enabled[a] = true
	}
	targets := skillTargets()
	for _, a := range []string{"codex", "claude", "comate"} {
		s := agentState{Enabled: enabled[a]}
		if _, err := exec.LookPath(a); err == nil {
			s.Installed = true
			if a != "comate" {
				s.MCPRegistered = mcpRegistered(a)
			}
		}
		if dir, ok := targets[a]; ok {
			if p := filepath.Join(dir, "hearth", "SKILL.md"); fileExists(p) {
				s.SkillPath = p
			}
		}
		st.Agents[a] = s
	}
	st.DaemonRunning = client.DaemonPID(home) != 0

	add := func(n nextStep) { st.Next = append(st.Next, n) }
	if st.WorkspaceSource == "none" && st.SharedHome {
		add(nextStep{ID: "workspace", Why: "this account's home may be shared by several people; keep this person's state in their own directory",
			AskUser: "Where do you keep your work on this machine?", Command: "mkdir -p <dir> && hearth init <dir> && cd <dir>"})
	}
	cfg, err := client.LoadConfig(home)
	if err != nil {
		server := client.DefaultServer(home)
		if server == "" {
			server = "<url>"
		} else {
			st.Server, st.Registration = server, client.Registration(home, server)
			if ok, err := probeServer(home, server); !ok {
				if n, isCert := certStep(server, err); isCert {
					add(n)
				}
			}
		}
		cmd := wsPrefix(st.Workspace) + "hearth setup"
		ask := ""
		if server == "<url>" {
			cmd += " --server <url>"
			ask = "the server URL (from the install command)"
		}
		if id, ok := client.DetectIdentity(home); ok && id.Email != "" {
			st.Email = id.Email // the account setup will use
		} else {
			cmd += " --email <email>"
			ask = strings.TrimPrefix(ask+" and the account email", " and ")
		}
		switch st.Registration {
		case "invite":
			cmd += "   # new account: add --invite <code>"
			ask = strings.TrimPrefix(ask+" and, for a new account, the invite code from the admin", " and ")
		case "closed":
			cmd += "   # this server does not accept new accounts"
		}
		add(nextStep{ID: "login", UserTerminal: true,
			Why:     "log in and restore sessions, skills and credentials (a new account is created if the server allows it); the master password must be typed by the user",
			AskUser: ask,
			Command: cmd})
		return st
	}
	st.LoggedIn, st.Server, st.Email, st.Device = true, cfg.Server, cfg.Email, cfg.DeviceName
	reach, perr := probeServer(home, cfg.Server)
	st.ServerReachable = &reach
	if n, isCert := certStep(cfg.Server, perr); isCert {
		add(n)
		return st
	}
	e, err := engine()
	if err != nil {
		add(nextStep{ID: "login", UserTerminal: true, Why: "local login state is incomplete: " + err.Error(),
			Command: wsPrefix(st.Workspace) + "hearth setup --server " + cfg.Server + " --email " + cfg.Email})
		return st
	}
	if reach {
		if teams, err := e.API.Teams(); err == nil {
			st.Teams = teams
		}
		if all, err := e.List(); err == nil {
			n := len(all)
			st.Sessions = &n
		} else if client.StatusOf(err) == 401 {
			add(nextStep{ID: "login", UserTerminal: true, Why: "the login expired or this device was revoked",
				Command: wsPrefix(st.Workspace) + "hearth setup --server " + cfg.Server + " --email " + cfg.Email})
			return st
		}
	}
	for _, a := range []string{"codex", "claude"} {
		if s := st.Agents[a]; s.Installed && s.Enabled && !s.MCPRegistered {
			add(nextStep{ID: "mcp", Why: "let Codex and Claude Code search skills on demand", Command: "hearth lib mcp-install"})
			break
		}
	}
	for _, a := range []string{"codex", "claude"} {
		if s := st.Agents[a]; s.Installed && s.Enabled && s.SkillPath == "" {
			add(nextStep{ID: "skill", Why: "install the hearth skill so the user can just ask their agent", Command: "hearth onboard install-skill"})
			break
		}
	}
	// Software and commands of the restore recipes: the agent goes
	// through them with the user (hearth recipe plan).
	if plan, err := e.Plan(); err == nil {
		var names []string
		for _, p := range plan {
			if p.Status != "todo" || (p.Type == "check" && p.Why == "optional") {
				continue
			}
			st.RecipeTodo++
			if p.Type == "manual" {
				st.RecipeManual++
			}
			names = append(names, planLabel(p))
		}
		if st.RecipeTodo > 0 {
			add(nextStep{ID: "recipes", Why: fmt.Sprintf("%d step(s) to set up this machine's software (%s)", st.RecipeTodo, joinMax(names, 4)),
				AskUser: "go through them together: show each step, run its check, install or run it with the user's ok, then mark it done",
				Command: "hearth recipe plan --json   # per step: check, do (hearth docs env explains each tool), hearth recipe done <key>"})
		}
	}
	if !st.DaemonRunning {
		add(nextStep{ID: "daemon", Why: "background sync is not running", Command: "hearth daemon start"})
	}
	// The daemon records new items by itself; what is left needs a choice
	// (changed copies, credentials while not automatic).
	n := len(e.LearnPending())
	st.LearnNew = &n
	if n > 0 && !client.LearnSnoozed(home) {
		why := fmt.Sprintf("%d item(s) on this machine need your choice (changed since recorded, or credentials)", n)
		if !client.AutoLearnOn(home) {
			why = fmt.Sprintf("%d credential(s), config(s) or setup item(s) on this machine are not recorded yet", n)
		}
		add(nextStep{ID: "learn", Why: why,
			AskUser: "for each: all machines / only machines like this / never; and any other step they always do",
			Command: "hearth learn --list --json   # then: hearth learn --accept <ids> --here <ids> --never <ids> [--step '$ cmd']"})
	}
	return st
}

func wsPrefix(ws string) string {
	if ws == "" {
		return ""
	}
	return "cd " + ws + " && "
}

func serverReachable(home, server string) bool { ok, _ := probeServer(home, server); return ok }

// probeServer checks /healthz; the error explains a failure (e.g. an
// untrusted self-signed certificate).
func probeServer(home, server string) (bool, error) {
	hc, err := client.HTTPClient(home)
	if err != nil {
		return false, err
	}
	hc.Timeout = 5 * time.Second
	resp, err := hc.Get(strings.TrimRight(server, "/") + "/healthz")
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// certStep explains how to trust a self-signed server when TLS fails.
func certStep(server string, err error) (nextStep, bool) {
	if err == nil || !strings.Contains(err.Error(), "x509") {
		return nextStep{}, false
	}
	return nextStep{ID: "ca", Why: "this machine does not trust the server's certificate (" + err.Error() + ")",
		AskUser: "the install command from the server admin (it pins the server key)",
		Command: "curl -fsSL -k --pinnedpubkey '<pin>' " + strings.TrimRight(server, "/") + "/install.sh | sh -s -- --install-only   # stores the CA; or: hearth login --ca-cert <file>"}, true
}

func onboardCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "onboard", Short: "Machine setup status and next steps (used by the hearth agent skill)"}
	status := &cobra.Command{Use: "status", Short: "What is set up on this machine and what to do next",
		RunE: func(*cobra.Command, []string) error {
			st := collectOnboard()
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				return enc.Encode(st)
			}
			yes := func(b bool) string {
				if b {
					return "yes"
				}
				return "no"
			}
			fmt.Printf("hearth %s (%s)\n", st.Version, st.Binary)
			ws := st.Workspace
			if ws == "" {
				ws = "(none)"
			}
			fmt.Printf("workspace: %s [%s]\nmachine:   %s; profiles %v\n", ws, st.WorkspaceSource, st.Machine, st.Profiles)
			if st.LoggedIn {
				fmt.Printf("account:   %s on %s (device %s), server reachable: %s\n", st.Email, st.Server, st.Device, yes(st.ServerReachable != nil && *st.ServerReachable))
			} else {
				fmt.Println("account:   not logged in")
			}
			for _, a := range []string{"codex", "claude"} {
				s := st.Agents[a]
				fmt.Printf("%-10s installed %s, enabled %s, skill search %s, setup skill %s\n", a+":", yes(s.Installed), yes(s.Enabled), yes(s.MCPRegistered), yes(s.SkillPath != ""))
			}
			fmt.Printf("sync:      daemon %s", yes(st.DaemonRunning))
			if st.Sessions != nil {
				fmt.Printf(", %d sessions on the server", *st.Sessions)
			}
			fmt.Println()
			if len(st.Next) == 0 {
				fmt.Println("\nall set")
			}
			for i, n := range st.Next {
				who := ""
				if n.UserTerminal {
					who = " (run it yourself in a terminal: it asks for your master password)"
				}
				fmt.Printf("\n%d. %s%s\n   %s\n", i+1, n.Why, who, n.Command)
			}
			return nil
		}}
	status.Flags().BoolVar(&asJSON, "json", false, "JSON for agents")
	install := &cobra.Command{Use: "install-skill", Short: "Install the hearth skill into every agent on this machine (--print shows it)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if p, _ := cmd.Flags().GetBool("print"); p {
				fmt.Print(skills.Hearth)
				return nil
			}
			quiet, _ := cmd.Flags().GetBool("quiet")
			return installSetupSkill(!quiet)
		}}
	install.Flags().Bool("print", false, "print the skill instead of installing it")
	install.Flags().Bool("quiet", false, "only print the hint")
	c.AddCommand(status, install)
	return c
}

const skillMarker = skills.Marker

// installSetupSkill installs every bundled skill into every agent found on
// this machine and tells the user how to use them. Disable with
// HEARTH_NO_SKILLS=1.
func installSetupSkill(verbose bool) error {
	_, err := installSetupSkillTo(verbose, true)
	return err
}

// installSetupSkillTo installs the hearth skill; with hint it prints where
// and how to use it. Returns the agents that have it.
func installSetupSkillTo(verbose, hint bool) ([]string, error) {
	if os.Getenv("HEARTH_NO_SKILLS") == "1" {
		return nil, nil
	}
	targets := skillTargets()
	var agents []string
	for a := range targets {
		agents = append(agents, a)
	}
	sort.Strings(agents)
	installed := map[string]bool{}
	for _, agent := range agents {
		for _, sk := range skills.All() {
			p := filepath.Join(targets[agent], sk.Name, "SKILL.md")
			content := sk.Content + "\n" + skillMarker + "\n"
			if fi, err := os.Lstat(filepath.Dir(p)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				fmt.Printf("%-6s %s is a symlink managed elsewhere; left alone\n", agent, filepath.Dir(p))
				continue
			}
			if cur, err := os.ReadFile(p); err == nil {
				if string(cur) == content {
					installed[agent] = true
					continue
				}
				// Copies from before the marker existed are recognised by
				// their heading.
				if !strings.Contains(string(cur), skillMarker) {
					fmt.Printf("%-6s %s exists and was not installed by hearth; left alone\n", agent, p)
					continue
				}
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return nil, err
			}
			if err := writeFileAtomicCLI(p, []byte(content)); err != nil {
				return nil, err
			}
			installed[agent] = true
			if verbose {
				fmt.Printf("%-6s %s\n", agent, p)
			}
		}
	}
	// Bundled skills that ended up in the user's library (an earlier
	// `hearth learn` imported them) would be rendered as stale copies.
	if home := client.Home(); home != "" {
		dir := library.Library{Root: client.LibraryDir(home)}.SkillsDir()
		entries, _ := os.ReadDir(dir)
		for _, en := range entries {
			if en.Type()&os.ModeSymlink != 0 || !en.IsDir() {
				continue
			}
			if md, err := os.ReadFile(filepath.Join(dir, en.Name(), "SKILL.md")); err == nil && skills.Bundled(md) {
				if err := os.RemoveAll(filepath.Join(dir, en.Name())); err == nil {
					fmt.Printf("removed bundled skill %s from your library (it ships with hearth)\n", en.Name())
				}
			}
		}
	}
	// Skills earlier versions installed (now merged into "hearth").
	for _, agent := range agents {
		for _, name := range skills.Retired {
			dir := filepath.Join(targets[agent], name)
			// A symlinked skill points at a copy the user manages elsewhere
			// (e.g. a git repo): never delete through it.
			if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
				continue
			}
			p := filepath.Join(dir, "SKILL.md")
			if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
				continue
			}
			cur, err := os.ReadFile(p)
			if err != nil || !skills.Bundled(cur) {
				continue
			}
			if err := os.Remove(p); err == nil {
				os.Remove(dir) // only if now empty
				if verbose {
					fmt.Printf("%-6s removed old %s (merged into hearth)\n", agent, dir)
				}
			}
		}
	}
	if len(installed) == 0 {
		fmt.Println("skills: no coding agent found on this machine (codex, claude, comate); run `hearth onboard install-skill` after installing one")
		return nil, nil
	}
	var names []string
	for a := range installed {
		names = append(names, agentLabel(a))
	}
	sort.Strings(names)
	if !hint {
		return names, nil
	}
	fmt.Printf("skill: hearth installed for %s.\n", strings.Join(names, ", "))
	fmt.Println("  Ask your agent \"用 hearth\": it checks the login, guides you through it if needed, then helps,")
	fmt.Println("  e.g. \"恢复上一台机器的会话\", \"不要上传 Claude 的会话\". Restart the agent if it was already open.")
	return names, nil
}

func agentLabel(a string) string {
	switch a {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "comate":
		return "Comate"
	}
	return a
}

func writeFileAtomicCLI(p string, b []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
