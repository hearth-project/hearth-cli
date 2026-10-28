package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
)

func shellCmd() *cobra.Command {
	c := &cobra.Command{Use: "shell", Short: "Your own lines of ~/.bashrc / ~/.zshrc / ~/.bash_aliases, kept encrypted for new machines ([shell] in config.toml)"}
	c.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print what Hearth keeps of each rc file on this machine (your lines, without the distribution default and Hearth's blocks)",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			if !client.ShellOn(home) {
				fmt.Println("off ([shell] sync = false)")
				return nil
			}
			for _, f := range client.ShellFiles(home) {
				own := f.OwnShell()
				if own == "" {
					fmt.Printf("== %s: nothing of yours\n", f.Path)
					continue
				}
				fmt.Printf("== %s (stored as secret %s)\n%s\n\n", f.Path, f.Secret, own)
			}
			return nil
		},
	}, &cobra.Command{
		Use:   "save",
		Short: "Upload the changed rc files now (the background sync does it every 20 min)",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			saved, err := e.SaveShell()
			for _, s := range saved {
				fmt.Printf("%s saved (%d paragraph(s), encrypted)\n", s.File.Path, s.Paragraphs)
			}
			if err == nil && len(saved) == 0 {
				fmt.Println("nothing changed since the last upload")
			}
			return err
		},
	}, &cobra.Command{
		Use:   "restore",
		Short: "Write the stored lines as snippets in <hearth home>/shell and source them from the rc files (setup does this)",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			rs, err := e.RestoreShell()
			for _, r := range rs {
				fmt.Printf("%s: %d paragraph(s), sourced from %s\n", r.File.Path, r.Paragraphs, r.Hooked)
				for _, m := range r.Missing {
					fmt.Printf("  skipped, not on this machine: source %s\n", m)
				}
			}
			if err == nil && len(rs) == 0 {
				fmt.Println("no shell lines stored yet (hearth shell save on the machine that has them)")
			} else if err == nil {
				fmt.Println("open a new shell to use them")
			}
			return err
		},
	})
	return c
}
