package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
)

// siteCmd shows and refreshes the organisation defaults from the server.
func siteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "site",
		Short: "Organisation defaults published on the server (recipes, files, learn settings)",
		Long: "The server admin can publish defaults for every user in the server's site directory\n" +
			"(hearth server --site, default <data>/site): recipes/*.toml, files/..., learn.json.\n" +
			"Clients fetch them at setup and with the library sync; a recipe or file of the same\n" +
			"name in your own library wins.",
	}
	for _, on := range []bool{false, true} {
		on := on
		use, short := "disable <recipe>", "Do not apply a site recipe for you (synced choice)"
		if on {
			use, short = "enable <recipe>", "Apply a site recipe you disabled"
		}
		c.AddCommand(&cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				if err := client.SetSiteRecipe(client.Home(), args[0], on); err != nil {
					return err
				}
				fmt.Printf("site recipe %s %s\n", args[0], map[bool]string{true: "enabled", false: "disabled"}[on])
				return nil
			}})
	}
	var server string
	var quiet bool
	pull := &cobra.Command{
		Use: "pull", Short: "Fetch the site defaults now (works before login with --server)",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			var r client.SiteResult
			var err error
			if server != "" {
				if err := os.MkdirAll(home, 0o700); err != nil {
					return err
				}
				r, err = client.SiteSyncFrom(home, server)
			} else {
				e, eerr := engine()
				if eerr != nil {
					return fmt.Errorf("%w (or pass --server <url> before logging in)", eerr)
				}
				r, err = e.SiteSync()
			}
			if err != nil {
				return err
			}
			if !quiet {
				fmt.Println("site defaults:", r.Op)
			}
			return nil
		},
	}
	pull.Flags().StringVar(&server, "server", "", "server URL (default: the one you are logged in to)")
	pull.Flags().BoolVar(&quiet, "quiet", false, "print nothing unless it fails")
	c.AddCommand(pull, &cobra.Command{
		Use: "status", Short: "List the site defaults on this machine",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			dir := client.SiteDir(home)
			n := 0
			filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					r, _ := filepath.Rel(dir, p)
					fmt.Println(" ", r)
					n++
				}
				return nil
			})
			if n == 0 {
				fmt.Println("no site defaults (the server publishes none, or run: hearth site pull)")
			}
			if d := client.SiteDisabled(home); len(d) > 0 {
				fmt.Println("disabled for you:", d)
			}
			return nil
		},
	})
	return c
}
