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
	c.AddCommand(add,
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
