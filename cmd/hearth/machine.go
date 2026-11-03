package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/machine"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// printMachine shows the facts and active profiles of this machine.
func printMachine(home, indent string) {
	m, err := client.Machine(home)
	if err != nil {
		fmt.Printf("%smachine: %v\n", indent, err)
		return
	}
	prof := "none"
	if n := m.Names(); len(n) > 0 {
		prof = strings.Join(n, ", ")
	}
	fmt.Printf("%smachine: %s; profiles: %s", indent, m.Facts.Summary(), prof)
	if ws := paths.Workspace(); ws != "" {
		fmt.Printf("; workspace %s", ws)
	}
	fmt.Println()
	if len(m.File.Profiles) > 0 && len(m.Profiles) == 0 {
		fmt.Printf("%s  no profile matches; steps limited to profiles are skipped (hearth machine add|use)\n", indent)
	}
}

func machineCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "machine",
		Short: "Kinds of machines (H800, GB200, Mac, ...) with their own setup steps and working directory",
		Long: "Profiles live in the synced library (library/machines.toml) and match machines by os, arch, gpu,\n" +
			"hostname or label ($HEARTH_LABELS). Recipe steps without conditions run everywhere (common habits);\n" +
			"steps with when = { profile = \"h800\" } (or gpu / arch / os / hostname / label) only where they match.\n" +
			"A profile's workspace becomes ${WORKSPACE} on matching machines unless `hearth init` set one.",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			printMachine(home, "")
			m, err := client.Machine(home)
			if err != nil {
				return err
			}
			for _, p := range m.File.Profiles {
				mark := " "
				for _, a := range m.Profiles {
					if a.Name == p.Name {
						mark = "*"
					}
				}
				fmt.Printf("%s %-16s %-40s %s\n", mark, p.Name, matchString(p.Match), p.Workspace)
			}
			return nil
		},
	}
	var match map[string]string
	var workspace, desc string
	var here bool
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update a profile (--here: match machines like this one)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			home := client.Home()
			m, err := client.Machine(home)
			if err != nil {
				return err
			}
			p, _ := m.File.Get(a[0])
			p.Name = a[0]
			if here {
				p.Match = machine.Suggest(m.Facts).Match
			}
			if len(match) > 0 {
				if p.Match == nil {
					p.Match = map[string]string{}
				}
				for k, v := range match {
					p.Match[k] = v
				}
			}
			if workspace != "" {
				p.Workspace = workspace
			}
			if desc != "" {
				p.Description = desc
			}
			m.File.Set(p)
			if err := m.File.Save(client.MachinesPath(home)); err != nil {
				return err
			}
			fmt.Printf("profile %s: %s %s (syncs with your library)\n", p.Name, matchString(p.Match), p.Workspace)
			if !m.Facts.Matches(p) {
				fmt.Println("note: this machine does not match it; `hearth machine use " + p.Name + "` applies it here anyway")
			}
			return nil
		},
	}
	add.Flags().StringToStringVar(&match, "match", nil, "conditions, e.g. --match gpu=h800,h100 --match arch=x86_64 (keys: "+strings.Join(machine.MatchKeys, ", ")+")")
	add.Flags().StringVar(&workspace, "workspace", "", "working directory on these machines, e.g. /mnt/ssd/$USER/work")
	add.Flags().StringVar(&desc, "description", "", "description")
	add.Flags().BoolVar(&here, "here", false, "match machines like this one (by GPU model, else OS and architecture)")
	var listAll, listJSON bool
	list := &cobra.Command{Use: "list", Short: "List your machines, architecture and agent readiness", RunE: func(*cobra.Command, []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		ds, err := e.API.Devices()
		if err != nil {
			return err
		}
		if listJSON {
			return printJSON(machineRows(e, ds, listAll))
		}
		t := &table{}
		t.add("MACHINE", "STATE", "FACTS", "ENVIRONMENT", "CHECKED", "SSH HOST")
		aliases := deviceAliases(e, ds)
		hidden := 0
		for _, d := range ds {
			if d.Status != "" && d.Status != "active" && !listAll {
				hidden++
				continue
			}
			state := "offline"
			if d.Status != "active" {
				state = d.Status
			} else if d.Online {
				state = "online"
			}
			facts, _ := d.Machine["facts"].(map[string]any)
			fact := "unknown"
			if facts != nil {
				fact = fmt.Sprintf("%v/%v", facts["os"], facts["arch"])
				if g, ok := facts["gpu"].(string); ok && g != "" {
					fact += " " + g
				}
			}
			ag := envSummary(d.Env)
			restore := aliases[d.ID]
			if d.Status != "active" {
				ag, restore = "-", "-"
			}
			checked := "-"
			if at, _ := d.Env["checked_at"].(float64); at > 0 {
				checked = agoUnix(int64(at))
			}
			t.add(client.DeviceLabel(d), state, fact, ag, checked, restore)
		}
		t.fit()
		t.print()
		if hidden > 0 {
			fmt.Printf("%d revoked machine(s) hidden (--all)\n", hidden)
		}
		fmt.Println("details: hearth machine inspect <machine>; fix from here: ask your agent \"恢复 <machine> 的 Codex 环境\"")
		return nil
	}}
	list.Flags().BoolVar(&listAll, "all", false, "also revoked machines")
	list.Flags().BoolVar(&listJSON, "json", false, "machine-readable (the macOS menu bar app reads it)")
	inspect := &cobra.Command{Use: "inspect <machine>", Short: "Show a machine's public health report", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		ds, err := e.API.Devices()
		if err != nil {
			return err
		}
		d, err := client.FindDevice(ds, a[0])
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(d, "", "  ")
		fmt.Println(string(b))
		return nil
	}}
	check := &cobra.Command{Use: "check", Short: "Check this machine's environment now and report it (the daemon does it every 5 minutes)", RunE: func(*cobra.Command, []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		client.FreshChecks = true
		h, err := reportHealthOf(e)
		if err != nil {
			return err
		}
		for _, name := range []string{"codex", "codex-mixin", "claude", "node"} {
			th, ok := h.Tools[name]
			if !ok {
				continue
			}
			where := "not found (PATH, login shell PATH, ~/.local/bin, nvm, Homebrew, ChatGPT / Codex / Codex Mixin apps, running processes, launchd agents)"
			if th.Installed {
				where = th.Version + "  " + th.Path
			}
			note := ""
			if a, ok := h.Agents[name]; ok && !a.Used {
				note = "  (not one of your agents lately: not checked)"
			}
			fmt.Printf("  %-12s %s%s\n", name, where, note)
		}
		ds, err := e.API.Devices()
		if err != nil {
			return err
		}
		for _, d := range ds {
			if d.ID == e.Cfg.DeviceID {
				fmt.Println("environment:", envSummary(d.Env))
				if probs, _ := d.Env["problems"].([]any); len(probs) > 1 {
					for _, p := range probs {
						if m, ok := p.(map[string]any); ok {
							fmt.Println("  -", envProblemText(m))
						}
					}
				}
			}
		}
		return nil
	}}
	var cmdJSON bool
	installCmd := &cobra.Command{Use: "install-command", Short: "Print a one-time command that installs hearth on a new machine and logs it in (30 minutes, once)", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		ic, err := e.MakeInstallCommand()
		if err != nil {
			return err
		}
		if cmdJSON {
			return printJSON(ic)
		}
		fmt.Println(ic.Command)
		if ic.OneTime {
			fmt.Fprintln(os.Stderr, "Run it on the new machine within 30 minutes; it works once.")
		} else {
			fmt.Fprintln(os.Stderr, "Run it on the new machine; it asks for your master password.")
		}
		return nil
	}}
	installCmd.Flags().BoolVar(&cmdJSON, "json", false, "machine-readable (the macOS menu bar app reads it)")
	c.AddCommand(list, inspect, check, add, installCmd,
		&cobra.Command{Use: "rm <name>", Short: "Remove a profile", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			home := client.Home()
			m, err := client.Machine(home)
			if err != nil {
				return err
			}
			if !m.File.Remove(a[0]) {
				return fmt.Errorf("no profile %q", a[0])
			}
			return report(m.File.Save(client.MachinesPath(home)), "removed profile "+a[0])
		}},
		&cobra.Command{Use: "use [name...]", Short: "Apply profiles to this machine even if they do not match (no names: clear)", RunE: func(_ *cobra.Command, a []string) error {
			home := client.Home()
			m, err := client.Machine(home)
			if err != nil {
				return err
			}
			for _, n := range a {
				if _, ok := m.File.Get(n); !ok {
					return fmt.Errorf("no profile %q (hearth machine add %s)", n, n)
				}
			}
			if err := client.SetChosenProfiles(home, a); err != nil {
				return err
			}
			printMachine(home, "")
			return nil
		}},
		&cobra.Command{Use: "facts", Short: "Print the detected facts as JSON", RunE: func(*cobra.Command, []string) error {
			b, _ := jsonMarshalIndent(client.Facts())
			os.Stdout.Write(append(b, '\n'))
			return nil
		}},
	)
	return c
}

func matchString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	if len(parts) == 0 {
		return "(manual: machine use)"
	}
	return strings.Join(parts, " ")
}

func jsonMarshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

// envSummary is the server's environment check in one cell.
func envSummary(env map[string]any) string {
	st, _ := env["status"].(string)
	probs, _ := env["problems"].([]any)
	switch st {
	case "ok":
		return "ok"
	case "", "unknown":
		return "not reported"
	}
	var first string
	if len(probs) > 0 {
		if p, ok := probs[0].(map[string]any); ok {
			first = envProblemText(p)
		}
	}
	if len(probs) > 1 {
		return fmt.Sprintf("%d problems: %s, …", len(probs), first)
	}
	return first
}

func envProblemText(p map[string]any) string {
	s := func(k string) string { v, _ := p[k].(string); return v }
	n, _ := p["behind"].(float64)
	tool := s("tool")
	switch s("code") {
	case "missing":
		return tool + " not installed"
	case "outdated":
		more := ""
		if m, _ := p["more"].(bool); m {
			more = "≥"
		}
		return fmt.Sprintf("%s %s%d behind (%s → %s)", tool, more, int(n), s("have"), s("latest"))
	case "doctor":
		return tool + " check failed: " + s("detail")
	case "integration":
		return tool + " not connected to hearth"
	case "node":
		return "node too old for codex (" + s("have") + ")"
	case "steps":
		return fmt.Sprintf("%d restore step(s) left", int(n))
	case "hearth":
		return "hearth " + s("have") + " → " + s("latest")
	case "bypass":
		return "codex does not go through codex-mixin: " + s("detail")
	case "unreachable":
		return "codex cannot reach " + s("have") + ": " + s("detail")
	case "no_login":
		return "codex has no login: " + s("detail") + " has no " + s("have") + " (Codex Desktop asks to log in)"
	case "stale_app_server":
		return fmt.Sprintf("%d codex app-server process(es) still use CODEX_HOME=%s from before the switch: hearth agents restart-app-server", int(n), s("detail"))
	case "ssh_down":
		return "remote ssh does not work: " + s("detail")
	case "ssh_hostkey":
		return "remote ssh answers with another host key"
	case "ssh_blocked":
		return "sshd works here, but the server cannot reach " + s("have")
	case "ssh_nat":
		return "the server cannot reach " + s("have") + ": the machine's requests come from " + s("detail") + ", an address sshd does not listen on (NAT or a proxy): set [ssh] address there, or map a port"
	case "shared_home":
		return "your codex-mixin login is in the shared " + s("detail") + ", everyone on this account falls back to it: hearth agents isolate codex"
	}
	return s("code")
}

// reportHealth runs the environment check and sends it to the server.
func reportHealth(e *client.Engine) error { _, err := reportHealthOf(e); return err }

func reportHealthOf(e *client.Engine) (client.HealthReport, error) {
	h := e.MachineHealth()
	return h, e.API.HeartbeatReport(map[string]any{"facts": h.Machine, "profiles": h.Profiles, "workspace": h.Workspace},
		map[string]any{"status": h.Status, "client": h.Client, "agents": h.Agents, "tools": h.Tools, "recipe_todo": h.RecipeTodo,
			"recipe_manual": h.RecipeManual, "todo_steps": h.TodoSteps, "issues": h.Issues, "ssh": h.SSH, "checked_at": h.CheckedAt})
}

// reportHealthQuiet reports after a change (a restore step done); a
// failure only means the daemon reports it a few minutes later.
func reportHealthQuiet(e *client.Engine) { _ = reportHealth(e) }

// machinesJSON is `hearth machine list --json`: what the console's machine
// list shows, for the macOS menu bar app and scripts. Public readiness data
// only (never credentials or contents).
type machinesJSON struct {
	Server   string        `json:"server"`
	Email    string        `json:"email"`
	Machines []machineJSON `json:"machines"`
}

type machineJSON struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`            // device (host) name
	Alias     string        `json:"alias,omitempty"` // the name the user gave it
	Label     string        `json:"label"`           // "alias (host)"
	Host      string        `json:"host"`            // SSH host name; sentences about the machine use it
	This      bool          `json:"this,omitempty"`  // the machine this command runs on
	Status    string        `json:"status"`          // active, revoked
	Online    bool          `json:"online"`
	LastSeen  int64         `json:"last_seen"`
	IP        string        `json:"ip,omitempty"`
	OS        string        `json:"os,omitempty"`
	Arch      string        `json:"arch,omitempty"`
	GPU       string        `json:"gpu,omitempty"`
	Version   string        `json:"version,omitempty"` // Hearth client version
	Env       string        `json:"env"`               // ok, warn, error, unknown
	Problems  []problemJSON `json:"problems"`
	SSH       string        `json:"ssh,omitempty"` // ok, blocked, down, off, checking, stale
	SSHAddr   string        `json:"ssh_addr,omitempty"`
	CheckedAt int64         `json:"checked_at,omitempty"`
}

type problemJSON struct {
	Code   string `json:"code"`
	Level  string `json:"level"`
	Tool   string `json:"tool,omitempty"`
	Have   string `json:"have,omitempty"`
	Latest string `json:"latest,omitempty"`
	Behind int    `json:"behind,omitempty"`
	Text   string `json:"text"` // English, as `hearth machine list` prints it
}

func machineRows(e *client.Engine, ds []api.Device, all bool) machinesJSON {
	out := machinesJSON{Server: e.Cfg.Server, Email: e.Cfg.Email, Machines: []machineJSON{}}
	hosts := deviceAliases(e, ds)
	for _, d := range ds {
		if d.Status != "" && d.Status != "active" && !all {
			continue
		}
		m := machineJSON{ID: d.ID, Name: d.Name, Alias: d.Alias, Label: client.DeviceLabel(d), Host: hosts[d.ID],
			This: d.ID == e.Cfg.DeviceID, Status: orDefault(d.Status, "active"), Online: d.Online, LastSeen: d.LastSeen,
			IP: d.LastIP, Env: "unknown", Problems: []problemJSON{}}
		if m.Host == "" {
			m.Host = d.Name
		}
		if f, _ := d.Machine["facts"].(map[string]any); f != nil {
			m.OS, _ = f["os"].(string)
			m.Arch, _ = f["arch"].(string)
			m.GPU, _ = f["gpu"].(string)
		}
		m.Version, _ = d.Health["client"].(string)
		if st, _ := d.Env["status"].(string); st != "" {
			m.Env = st
		}
		if at, _ := d.Env["checked_at"].(float64); at > 0 {
			m.CheckedAt = int64(at)
		}
		probs, _ := d.Env["problems"].([]any)
		for _, p := range probs {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			str := func(k string) string { v, _ := pm[k].(string); return v }
			n, _ := pm["behind"].(float64)
			m.Problems = append(m.Problems, problemJSON{Code: str("code"), Level: str("level"), Tool: str("tool"),
				Have: str("have"), Latest: str("latest"), Behind: int(n), Text: envProblemText(pm)})
		}
		if ssh, _ := d.Env["ssh"].(map[string]any); ssh != nil {
			m.SSH, _ = ssh["status"].(string)
			if a, _ := ssh["address"].(string); a != "" {
				m.SSHAddr = a
				if p, _ := ssh["port"].(float64); p > 0 {
					m.SSHAddr = fmt.Sprintf("%s:%d", a, int(p))
				}
			}
		}
		out.Machines = append(out.Machines, m)
	}
	return out
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
