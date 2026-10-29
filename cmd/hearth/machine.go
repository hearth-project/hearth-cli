package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

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
	var listAll bool
	list := &cobra.Command{Use: "list", Short: "List your machines, architecture and agent readiness", RunE: func(*cobra.Command, []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		ds, err := e.API.Devices()
		if err != nil {
			return err
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
			t.add(d.Name, state, fact, ag, checked, restore)
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
	c.AddCommand(list, inspect, check, add,
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
	}
	return s("code")
}

// reportHealth runs the environment check and sends it to the server.
func reportHealth(e *client.Engine) error { _, err := reportHealthOf(e); return err }

func reportHealthOf(e *client.Engine) (client.HealthReport, error) {
	h := e.MachineHealth()
	return h, e.API.HeartbeatReport(map[string]any{"facts": h.Machine, "profiles": h.Profiles, "workspace": h.Workspace},
		map[string]any{"status": h.Status, "client": h.Client, "agents": h.Agents, "tools": h.Tools, "recipe_todo": h.RecipeTodo,
			"recipe_manual": h.RecipeManual, "todo_steps": h.TodoSteps, "issues": h.Issues, "checked_at": h.CheckedAt})
}

// reportHealthQuiet reports after a change (a restore step done); a
// failure only means the daemon reports it a few minutes later.
func reportHealthQuiet(e *client.Engine) { _ = reportHealth(e) }
