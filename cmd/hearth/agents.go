package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
)

func agentsCmd() *cobra.Command {
	c := &cobra.Command{Use: "agents", Short: "Which coding agents hearth integrates with (default: codex, claude)"}
	c.AddCommand(
		&cobra.Command{Use: "list", Short: "Show supported and enabled agents", RunE: func(*cobra.Command, []string) error {
			on := map[string]bool{}
			for _, a := range client.EnabledAgents(client.Home()) {
				on[a] = true
			}
			homes := client.AgentHomes(client.Home())
			here := map[string]bool{}
			for _, a := range client.MachineAgents(client.Home()) {
				here[a] = true
			}
			for _, a := range client.SupportedAgents {
				state := "disabled"
				switch {
				case here[a]:
					state = "enabled"
				case on[a]:
					state = "enabled, not restored here (hearth agents pull " + a + ")"
				}
				h := homes[a]
				if h == "" {
					h = "(default)"
				}
				fmt.Printf("%-8s %-9s  %s\n", a, state, h)
			}
			if ws := client.WorkspaceOf(client.Home()); ws != "" {
				fmt.Println("workspace:", ws)
			}
			return nil
		}},
		&cobra.Command{Use: "enable <agent>", Short: "Enable an agent (synced setting)", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			return report(client.SetAgentEnabled(client.Home(), a[0], true), "enabled "+a[0]+"; now: "+strings.Join(client.EnabledAgents(client.Home()), ", "))
		}},
		&cobra.Command{Use: "home <agent> <dir>", Short: "Use a per-user config dir for an agent, e.g. '${WORKSPACE}/.codex' (synced)", Args: cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, a []string) error {
				if err := client.SetAgentHome(client.Home(), a[0], a[1]); err != nil {
					return err
				}
				fmt.Printf("%s now uses %s; recipe agent-homes exports it in your shells (hearth recipe apply)\n", a[0], a[1])
				return nil
			}},
		agentsPullCmd(),
		&cobra.Command{Use: "disable <agent>", Short: "Disable an agent", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			return report(client.SetAgentEnabled(client.Home(), a[0], false), "disabled "+a[0]+"; now: "+strings.Join(client.EnabledAgents(client.Home()), ", "))
		}},
	)
	return c
}

// agentsPullCmd restores an agent this machine skipped at setup (not used
// recently): its skills/agents, newest conversations and CLI.
func agentsPullCmd() *cobra.Command {
	last := defaultRestoreLast
	c := &cobra.Command{
		Use:   "pull <agent>",
		Short: "Restore an agent on this machine: skills/agents, newest conversations, its CLI (setup restores only recently used agents)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			home := client.Home()
			name := a[0]
			on := false
			for _, x := range client.EnabledAgents(home) {
				on = on || x == name
			}
			if !on {
				if err := client.SetAgentEnabled(home, name, true); err != nil {
					return err
				}
			}
			if err := client.AddMachineAgent(home, name); err != nil {
				return err
			}
			e, err := engine()
			if err != nil {
				return err
			}
			acts, err := e.Render(false)
			printActions(acts)
			if err != nil {
				return err
			}
			if err := restoreAll(e, name, false, last, nil); err != nil {
				return err
			}
			if plan, err := e.Plan(); err == nil {
				for _, p := range plan {
					if p.Status == "todo" && p.Name == name {
						fmt.Printf("%s CLI: %s (or ask your agent \"用 hearth 恢复这台机器的环境\"; guide: hearth docs env)\n", name, p.Do)
					}
				}
			}
			return nil
		},
	}
	c.Flags().IntVar(&last, "last", last, "restore this many newest conversations (0 = all)")
	return c
}
