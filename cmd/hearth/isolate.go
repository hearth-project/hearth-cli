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
				if ws := os.Getenv("HEARTH_WORKSPACE"); ws != "" {
					// A machine profile's workspace: the login still lives in ~/.hearth.
					return fmt.Errorf("this login lives in %s, not in the workspace: move it there first:  hearth init --move-login %s", home, ws)
				}
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
			fmt.Println("Your sessions through Hearth's sshd (hearth ssh, Codex Desktop over its ssh alias) use it.")
			if agent == "codex" {
				// --yes approved the move, not ending running sessions: ask (or print the command).
				if err := restartStaleAppServers(home, e.Cfg.Email, false); err != nil {
					fmt.Fprintln(os.Stderr, "app-servers:", err)
				}
			}
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

// restartStaleAppServers stops this person's Codex app-servers that still
// use the CODEX_HOME from before a switch (asking first unless yes). Codex
// Desktop starts a new one when it reconnects, and that one gets the new home.
func restartStaleAppServers(home, email string, yes bool) error {
	st := client.StaleAppServers(home, email)
	if len(st) == 0 {
		return nil
	}
	want := client.AgentHomeOf(home, "codex")
	fmt.Printf("%d Codex app-server process(es) of yours still use the old CODEX_HOME (they keep the home they started with):\n", len(st))
	inside := false
	for _, a := range st {
		mark := ""
		if a.Ancestor {
			mark, inside = "  (this session runs inside it)", true
		}
		fmt.Printf("  pid %-7d CODEX_HOME=%s  %s%s\n", a.PID, a.CodexHome, a.Command, mark)
	}
	if !yes {
		if !isTTY() {
			fmt.Println("stop them with: hearth agents restart-app-server --yes (Codex Desktop reconnects and starts one on " + want + ")")
			return nil
		}
		q := "Stop them? Codex Desktop reconnects and starts a new one on " + want
		if inside {
			q += "; this session ends and reconnects too"
		}
		fmt.Print(q + " [y/N] ")
		line, _ := stdinLines.ReadString('\n')
		if l := strings.ToLower(strings.TrimSpace(line)); l != "y" && l != "yes" {
			fmt.Println("left running: hearth agents restart-app-server stops them later")
			return nil
		}
	}
	left, err := client.StopAppServers(st, 5*time.Second)
	fmt.Printf("stopped %d of %d; reconnect Codex Desktop to this machine if it does not by itself\n", len(st)-len(left), len(st))
	for _, a := range left {
		fmt.Printf("  still running: pid %d (kill %d)\n", a.PID, a.PID)
	}
	return err
}

func agentsRestartAppServerCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "restart-app-server",
		Short: "Stop your Codex app-servers that still use the CODEX_HOME from before Hearth switched it (Codex Desktop starts new ones)",
		Long: `Codex Desktop starts a long-running codex app-server on a remote machine and
it keeps the CODEX_HOME of the session that started it. After Hearth changes
your agent home (hearth agents isolate, a new identity block), those servers
still use the old home until they restart. This stops the ones of your
sessions (HEARTH_USER) whose CODEX_HOME is the account-wide ~/.codex or a home
Hearth set before; other people's processes are not touched.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if len(client.StaleAppServers(e.Home, e.Cfg.Email)) == 0 {
				fmt.Println("no app-server of yours uses an old CODEX_HOME (now " + client.AgentHomeOf(e.Home, "codex") + ")")
				return nil
			}
			return restartStaleAppServers(e.Home, e.Cfg.Email, yes)
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask")
	return c
}

func agentsWhereCmd() *cobra.Command {
	return &cobra.Command{Use: "where <agent>", Short: "Print the config dir an agent uses for you here (e.g. CODEX_HOME=$(hearth agents where codex))", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			fmt.Println(client.AgentHomeOf(client.Home(), a[0]))
			return nil
		}}
}
