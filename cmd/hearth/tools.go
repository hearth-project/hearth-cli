package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/tools"
)

func toolsApprovals() string { return filepath.Join(client.Home(), "tools-approved.json") }

func toolsCmd() *cobra.Command {
	c := &cobra.Command{Use: "tools", Short: "Pin and install the agent CLIs themselves (codex, claude, custom)"}
	var version, check, install string
	var current bool
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a tool to the synced manifest (built in: " + strings.Join(tools.BuiltinNames(), ", ") + ")",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			t := tools.Tool{Name: a[0], Version: version, Check: check, Install: install}
			if current {
				v := tools.Installed(t)
				if v == "" || v == "unknown" {
					return fmt.Errorf("%s is not installed here (or its version cannot be read); use --version", a[0])
				}
				t.Version = v
			}
			m, err := client.LoadTools(client.Home())
			if err != nil {
				return err
			}
			if err := m.Set(t); err != nil {
				return err
			}
			if err := client.SaveTools(client.Home(), m); err != nil {
				return err
			}
			v := t.Version
			if v == "" {
				v = "any version"
			}
			fmt.Printf("added %s (%s); synced with your library\n", t.Name, v)
			return nil
		},
	}
	add.Flags().StringVar(&version, "version", "", "pin this version (x.y.z)")
	add.Flags().BoolVar(&current, "current", false, "pin the version installed on this machine")
	add.Flags().StringVar(&check, "check", "", "custom tool: shell command printing its version")
	add.Flags().StringVar(&install, "install", "", "custom tool: shell command installing it ($VERSION is set)")

	var yes, dry bool
	inst := &cobra.Command{
		Use:   "install",
		Short: "Install missing or mismatched tools from the manifest",
		RunE: func(*cobra.Command, []string) error {
			return installTools(yes, dry)
		},
	}
	inst.Flags().BoolVarP(&yes, "yes", "y", false, "approve custom install commands shown in the output")
	inst.Flags().BoolVar(&dry, "dry-run", false, "only show what would be installed")

	c.AddCommand(add, inst,
		&cobra.Command{Use: "list", Short: "Manifest versus this machine", RunE: func(*cobra.Command, []string) error {
			m, err := client.LoadTools(client.Home())
			if err != nil {
				return err
			}
			for _, t := range m.Tools {
				want := t.Version
				if want == "" {
					want = "any"
				}
				have := tools.Installed(t)
				if have == "" {
					have = "missing"
				}
				fmt.Printf("%-12s want %-12s have %s\n", t.Name, want, have)
			}
			return nil
		}},
		&cobra.Command{Use: "rm <name>", Short: "Remove a tool from the manifest", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			m, err := client.LoadTools(client.Home())
			if err != nil {
				return err
			}
			out := m.Tools[:0]
			for _, t := range m.Tools {
				if t.Name != a[0] {
					out = append(out, t)
				}
			}
			if len(out) == len(m.Tools) {
				return fmt.Errorf("%s is not in the manifest", a[0])
			}
			m.Tools = out
			return report(client.SaveTools(client.Home(), m), "removed "+a[0])
		}},
	)
	return c
}

func installTools(approve, dry bool) error {
	return installToolsWith(tools.SyncOptions{Approve: approve, DryRun: dry}, false)
}

func installToolsWith(o tools.SyncOptions, quiet bool) error {
	dry := o.DryRun
	m, err := client.LoadTools(client.Home())
	if err != nil {
		return err
	}
	ap, err := tools.LoadApprovals(toolsApprovals())
	if err != nil {
		return err
	}
	failed, pending := 0, 0
	for _, s := range tools.SyncWith(m, ap, o) {
		if quiet && (s.Action == "ok" || s.Action == "kept") {
			continue
		}
		line := fmt.Sprintf("tool %-12s %-14s %s", s.Tool.Name, s.Action, s.Installed)
		if s.Detail != "" && s.Action != "ok" {
			line += "  " + s.Detail
		}
		fmt.Println(line)
		switch s.Action {
		case "failed", "missing-npm":
			failed++
		case "needs-approval":
			pending++
		}
	}
	if pending > 0 {
		fmt.Println("custom install commands come from your synced library; review them and run `hearth tools install --yes`")
	}
	if !dry {
		if err := ap.Save(toolsApprovals()); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d tool(s) failed to install", failed)
	}
	return nil
}
