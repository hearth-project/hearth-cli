package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
)

// agentsIsolateCmd moves this person's agent home out of the account-wide
// default on a shared account (see client.IsolateAgentHome).
func agentsIsolateCmd() *cobra.Command {
	var dir string
	var yes bool
	c := &cobra.Command{
		Use:   "isolate <codex|claude>",
		Short: "Shared account: move your agent config and conversations into your workspace, so nobody else on the account uses your gateway login",
		Long: `On an account several people share, ~/.codex is the config every Codex
session falls back to. If your codex-mixin gateway and client key are there,
everyone whose shell does not set CODEX_HOME uses your login.

isolate copies your config (skills, rules, plugins, MCP servers, model
settings) and your conversations (the session scope decides which are yours)
to <home>/agent-homes/codex (inside your Hearth home: per machine, and apart
from the workspace's project .codex), has codex-mixin connect the new home with a client key
of its own, makes this machine's Hearth sessions use it, then hands the shared
default back to codex-mixin (uninstall-codex; else the gateway provider and
key are removed). A backup of the shared default's config goes to
<home>/backup. Other people's conversations stay where they are.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			home := client.Home()
			agent := a[0]
			if client.WorkspaceOf(home) == "" {
				return fmt.Errorf("no workspace here: isolate is for shared accounts (hearth init <dir> first)")
			}
			from := client.AgentHomeOf(home, agent)
			if !yes {
				if !isTTY() {
					return fmt.Errorf("moves %s's home %s into your workspace and removes your gateway login from it; run again with --yes", agent, from)
				}
				fmt.Printf("Move your %s home %s into your workspace (dir: %s) and remove your login from %s? [y/N] ", agent, from, orDefault(dir, client.OwnAgentHome(home, agent)), from)
				line, _ := stdinLines.ReadString('\n')
				if l := strings.ToLower(strings.TrimSpace(line)); l != "y" && l != "yes" {
					return fmt.Errorf("cancelled")
				}
			}
			e, err := engine()
			if err != nil {
				return err
			}
			running := client.DaemonPID(home) != 0
			if running {
				_ = client.StopDaemon(home, 30*time.Second) // it watches the old home
			}
			r, err := e.IsolateAgentHome(agent, dir, func(f string, x ...any) { fmt.Printf("  "+f+"\n", x...) })
			if running {
				if serr := startDaemon(); serr != nil {
					fmt.Fprintln(os.Stderr, "daemon:", serr)
				}
			}
			if err != nil {
				return err
			}
			if r.Already {
				fmt.Printf("%s already uses %s\n", agent, r.To)
				return nil
			}
			fmt.Printf("%s now uses %s on this machine (%d conversations copied", agent, r.To, r.Sessions)
			if r.Indexed > 0 {
				fmt.Printf(", %d listed in Codex", r.Indexed)
			}
			fmt.Println(")")
			if r.Cleaned != "" {
				fmt.Printf("%s: %s\n", r.From, r.Cleaned)
			}
			if r.Backup != "" {
				fmt.Println("backup of its old config:", r.Backup)
			}
			for _, n := range r.Notes {
				fmt.Println("note:", n)
			}
			fmt.Println("Your sessions through Hearth's sshd (hearth ssh, Codex Desktop over its ssh alias) use it; restart open Codex windows / app-servers on this machine.")
			if r.Sessions > 0 {
				fmt.Printf("Your old conversation files are still in %s (readable by everyone on the account); delete them there when the new home works.\n", r.From)
			}
			return nil
		},
	}
	c.Flags().StringVar(&dir, "dir", "", "new home (default <hearth home>/agent-homes/<agent>)")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask")
	return c
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func agentsWhereCmd() *cobra.Command {
	return &cobra.Command{Use: "where <agent>", Short: "Print the config dir an agent uses for you here (e.g. CODEX_HOME=$(hearth agents where codex))", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			fmt.Println(client.AgentHomeOf(client.Home(), a[0]))
			return nil
		}}
}
