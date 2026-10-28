package main

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// sessionScopeCmd controls which local sessions are this user's.
func sessionScopeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "scope",
		Short: "Which local sessions are yours (shared accounts: only those run inside your workspace are uploaded)",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			s := client.LoadScope(home)
			ws := paths.Workspace()
			switch {
			case s.All:
				fmt.Println("scope: every session on this account (hearth session scope mine-only to limit it)")
			case ws == "":
				fmt.Println("scope: every session (no workspace set; on a shared account run: hearth init <your dir>)")
			default:
				fmt.Printf("scope: sessions run inside %s", ws)
				for _, p := range s.Paths {
					fmt.Printf(", %s", p)
				}
				fmt.Println()
			}
			e, err := engine()
			if err != nil {
				return err
			}
			fs := e.ForeignSessions()
			if len(fs) == 0 {
				return nil
			}
			by := map[string]int{}
			for _, f := range fs {
				k := f.Agent + "  " + f.Cwd
				if f.Cwd == "" {
					k = f.Agent + "  (unknown directory) " + filepath.Dir(f.RelPath)
				}
				by[k]++
			}
			keys := make([]string, 0, len(by))
			for k := range by {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Printf("\nnot uploaded (%d files; other people's or outside your workspace):\n", len(fs))
			for _, k := range keys {
				fmt.Printf("  %4d  %s\n", by[k], k)
			}
			fmt.Println("\nyours too? hearth session scope add <dir>   (or, if nobody else uses this account: hearth session scope all)")
			return nil
		},
	}
	edit := func(f func(*client.Scope, []string) error) func(*cobra.Command, []string) error {
		return func(_ *cobra.Command, a []string) error {
			home := client.Home()
			s := client.LoadScope(home)
			if err := f(&s, a); err != nil {
				return err
			}
			return report(client.SaveScope(home, s), "scope updated (the daemon applies it on its next pass)")
		}
	}
	c.AddCommand(
		&cobra.Command{Use: "add <dir>...", Short: "Also upload sessions run in these directories", Args: cobra.MinimumNArgs(1),
			RunE: edit(func(s *client.Scope, a []string) error {
				for _, d := range a {
					abs, err := filepath.Abs(paths.Expand(d))
					if err != nil {
						return err
					}
					s.Paths = append(s.Paths, paths.Contract(abs))
				}
				return nil
			})},
		&cobra.Command{Use: "rm <dir>...", Short: "Stop uploading sessions of these directories", Args: cobra.MinimumNArgs(1),
			RunE: edit(func(s *client.Scope, a []string) error {
				drop := map[string]bool{}
				for _, d := range a {
					abs, _ := filepath.Abs(paths.Expand(d))
					drop[paths.Contract(abs)], drop[d] = true, true
				}
				var keep []string
				for _, p := range s.Paths {
					if !drop[p] {
						keep = append(keep, p)
					}
				}
				s.Paths = keep
				return nil
			})},
		&cobra.Command{Use: "all", Short: "Nobody else uses this account: upload every session", Args: cobra.NoArgs,
			RunE: edit(func(s *client.Scope, _ []string) error { s.All = true; return nil })},
		&cobra.Command{Use: "mine-only", Short: "Only upload sessions inside the workspace and added directories (default)", Args: cobra.NoArgs,
			RunE: edit(func(s *client.Scope, _ []string) error { s.All = false; return nil })},
		pruneForeignCmd(),
	)
	return c
}

func pruneForeignCmd() *cobra.Command {
	var yes bool
	var keep []string
	c := &cobra.Command{
		Use:   "prune",
		Short: "Delete sessions on the server that ran outside the uploading machine's workspace (other people's, uploaded before scoping)",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			var extra []string
			for _, k := range keep {
				abs, _ := filepath.Abs(paths.Expand(k))
				extra = append(extra, abs)
			}
			for _, p := range client.LoadScope(e.Home).Paths {
				extra = append(extra, paths.Expand(p))
			}
			fs, err := e.ForeignOnServer(extra)
			if err != nil {
				return err
			}
			if len(fs) == 0 {
				fmt.Println("nothing to prune")
				return nil
			}
			for _, f := range fs {
				cwd := f.Cwd
				if cwd == "" {
					cwd = "(unknown)"
				}
				fmt.Printf("%s  %-6s from %-40s cwd %s\n", f.ID[:12], f.Meta.Agent, f.Meta.Host, cwd)
			}
			if !yes {
				fmt.Printf("\n%d session(s) above would be deleted from the server (local files stay). Keep some with --keep <dir>; delete with --yes\n", len(fs))
				return nil
			}
			for _, f := range fs {
				if err := e.API.DeleteSession(f.ID); err != nil {
					return err
				}
			}
			fmt.Printf("deleted %d session(s) from the server; their machines will not upload them again\n", len(fs))
			return nil
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "delete them")
	c.Flags().StringSliceVar(&keep, "keep", nil, "directories whose sessions are yours after all (repeatable)")
	return c
}
