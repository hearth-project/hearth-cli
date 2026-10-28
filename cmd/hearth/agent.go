package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
)

// agentCmd exposes the per-agent integration registry: which agents have an
// integration, what they support, and cross-agent session import.
func agentCmd() *cobra.Command {
	c := &cobra.Command{Use: "agent", Short: "Agent integrations (Codex, Claude Code, ...)"}
	c.AddCommand(agentListCmd(), agentImportCmd())
	return c
}

func agentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List agent integrations and their capabilities",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			enabled := map[string]bool{}
			for _, a := range e.Adapters {
				enabled[a.Name()] = true
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "AGENT\tENABLED\tSESSIONS\tMEMORY\tTHREADS\tLIVE UI\tDELETE\tIMPORTS\tINDEX FIELDS")
			for _, in := range client.Integrations() {
				cap := in.Capabilities()
				imports := "-"
				if len(cap.ImportFrom) > 0 {
					imports = fmt.Sprint(cap.ImportFrom)
				}
				fmt.Fprintf(tw, "%s\t%v\t%v\t%v\t%v\t%v\t%v\t%s\t%s\n", in.Name(), enabled[in.Name()], cap.Sessions, cap.Memory, cap.Threads, cap.LiveUI, cap.DeleteSync, imports, fmt.Sprint(cap.IndexFields))
			}
			return tw.Flush()
		},
	}
}

func agentImportCmd() *cobra.Command {
	var from string
	var dryRun bool
	c := &cobra.Command{
		Use:   "import <agent> [session-file | hearth-session-id ...]",
		Short: "Import sessions of another agent into this agent (via its own converter)",
		Long: "Uses the target agent's own importer (e.g. Codex's externalAgentConfig/import)\n" +
			"so format details stay owned by that agent. Arguments are transcript files or hearth\n" +
			"session ids (restored first). With none, imports what the target agent detects for\n" +
			"the current directory.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runAgentImport(args[0], from, dryRun, args[1:])
		},
	}
	c.Flags().StringVar(&from, "from", "claude", "source agent to import from")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "only show what would be imported")
	return c
}

func runAgentImport(agent, from string, dryRun bool, args []string) error {
	e, err := engine()
	if err != nil {
		return err
	}
	in, ok := client.Integration(agent)
	if !ok {
		return fmt.Errorf("no integration for agent %q (hearth agent list)", agent)
	}
	allowed := false
	for _, s := range in.Capabilities().ImportFrom {
		if s == from {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Errorf("%s cannot import sessions from %s", agent, from)
	}
	var paths []string
	for _, a := range args {
		if _, err := os.Stat(a); err == nil {
			paths = append(paths, a)
			continue
		}
		rs, err := e.Resolve(a)
		if err != nil {
			return err
		}
		if rs.Meta.Agent != from {
			return fmt.Errorf("%s is a %s session; --from %s expected", short(rs.ID), rs.Meta.Agent, from)
		}
		r, err := e.Restore(rs, false)
		if err != nil {
			return err
		}
		if r.Path == "" {
			return fmt.Errorf("%s: %s %s", short(rs.ID), r.Action, r.Reason)
		}
		fmt.Printf("%s  %s %s\n", short(rs.ID), r.Action, r.Path)
		paths = append(paths, r.Path)
	}
	cwd, _ := os.Getwd()
	if dryRun {
		for _, p := range paths {
			fmt.Println("would import", p)
		}
		if len(paths) == 0 {
			fmt.Printf("would import the %s sessions %s detects for %s\n", from, agent, cwd)
		}
		return nil
	}
	res, live, err := in.Import(e, from, paths)
	failed := 0
	for _, r := range res {
		if r.Error != "" {
			failed++
			fmt.Printf("failed    %s: %s\n", r.Source, r.Error)
		} else {
			fmt.Printf("imported  %s -> %s thread %s  %s\n", r.Source, agent, r.Thread, r.Title)
		}
	}
	if err != nil {
		return err
	}
	if len(res) == 0 {
		fmt.Println("nothing to import (already imported, or no sessions found)")
	} else if !live && failed < len(res) {
		fmt.Println(codexDesktopHint)
	}
	if failed > 0 {
		return fmt.Errorf("%d sessions failed to import", failed)
	}
	return nil
}
