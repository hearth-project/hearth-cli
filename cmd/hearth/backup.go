package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/backup"
	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// backupCmd manages the user's upload selection (library/backup.conf).
func backupSelCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "backup",
		Short: "Choose what Hearth uploads (synced, .gitignore-like selection)",
		Long: "The selection lives in your synced config.toml ([backup] include/exclude), so every machine\n" +
			"follows the same choices. Lines select: @category, paths (~/ = home,\n" +
			"${WORKSPACE}/ = workspace, * ** ? wildcards, trailing / for directories);\n" +
			"\"!\" lines exclude and win over everything.\n\n" +
			"Categories: @<agent> and @<agent>.sessions / @<agent>.memory (e.g. @codex,\n" +
			"!@claude.sessions; the most specific line wins), @workspace @credentials,\n" +
			"and @sessions / @memory as fallbacks for agents without a line.",
	}
	c.AddCommand(backupShowCmd(), backupEditCmd(), backupStatusCmd(),
		backupToggleCmd("enable", true), backupToggleCmd("disable", false),
		backupLineCmd("add", "Back up a file, directory or pattern", false),
		backupLineCmd("exclude", "Never upload a file, directory or pattern", true))
	return c
}

func backupEngine() (*client.Engine, error) {
	e, err := engine()
	if err != nil {
		return nil, err
	}
	if created, err := e.EnsureBackupConfig(); err != nil {
		return nil, err
	} else if created {
		fmt.Println("created", client.BackupConfigPath(e.Home))
	}
	return e, nil
}

func backupShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show", Short: "Print the selection file",
		RunE: func(*cobra.Command, []string) error {
			e, err := backupEngine()
			if err != nil {
				return err
			}
			text := client.BackupText(e.Home)
			if text == "" {
				text = client.DefaultBackupConfig()
			}
			fmt.Print(text)
			fmt.Printf("\n# edit these in %s ([backup] section)\n", library.Library{Root: client.LibraryDir(e.Home)}.ConfigPath())
			return nil
		},
	}
}

func backupEditCmd() *cobra.Command {
	return &cobra.Command{
		Use: "edit", Short: "Open the selection in $EDITOR (synced on the next library sync)",
		RunE: func(*cobra.Command, []string) error {
			e, err := backupEngine()
			if err != nil {
				return err
			}
			ed := os.Getenv("VISUAL")
			if ed == "" {
				ed = os.Getenv("EDITOR")
			}
			if ed == "" {
				ed = "vi"
			}
			p := library.Library{Root: client.LibraryDir(e.Home)}.ConfigPath()
			if _, err := client.EnsureUserConfig(e.Home); err != nil {
				return err
			}
			cmd := exec.Command("sh", "-c", ed+` "$1"`, "sh", p)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return cmd.Run()
		},
	}
}

func backupToggleCmd(use string, on bool) *cobra.Command {
	verb := "Upload"
	if !on {
		verb = "Stop uploading"
	}
	return &cobra.Command{
		Use: use + " <category>", Short: verb + " a category: an agent (codex), agent.kind (claude.sessions), workspace, credentials",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cat := strings.TrimPrefix(strings.ToLower(args[0]), "@")
			known := false
			for _, k := range client.BackupCategories() {
				known = known || k == cat
			}
			if !known {
				return fmt.Errorf("unknown category %q; known: %s", cat, strings.Join(client.BackupCategories(), ", "))
			}
			e, err := backupEngine()
			if err != nil {
				return err
			}
			if err := client.SetBackupCategory(e.Home, cat, on); err != nil {
				return err
			}
			fmt.Printf("@%s %sd; synced in config.toml with the library\n", cat, use)
			if !on {
				fmt.Println("already uploaded data stays on the server; remove it with hearth session rm / hearth secret rm")
			}
			return nil
		},
	}
}

func backupLineCmd(use, short string, exclude bool) *cobra.Command {
	return &cobra.Command{
		Use: use + " <path-or-pattern> [...]", Short: short, Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			e, err := backupEngine()
			if err != nil {
				return err
			}
			for _, a := range args {
				line := portablePattern(a)
				added, err := client.AddBackupRule(e.Home, line, exclude)
				if err != nil {
					return err
				}
				if added {
					fmt.Println("added", line)
				} else {
					fmt.Println("already present:", line)
				}
			}
			return nil
		},
	}
}

// portablePattern writes absolute paths under home/workspace in their
// portable form, so the line means the same on every machine.
func portablePattern(p string) string {
	if strings.HasPrefix(p, "~") || strings.HasPrefix(p, "${") || strings.HasPrefix(p, "**") || !strings.HasPrefix(p, "/") {
		return p
	}
	trail := strings.HasSuffix(p, "/")
	c := paths.Contract(strings.TrimSuffix(p, "/"))
	if trail {
		c += "/"
	}
	return c
}

func backupStatusCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use: "status", Short: "Show what the selection uploads from this machine",
		RunE: func(*cobra.Command, []string) error {
			e, err := backupEngine()
			if err != nil {
				return err
			}
			cfg := client.BackupSelection(e.Home)
			var cats, off []string
			for _, c := range client.BackupCategories() {
				if cfg.Enabled(c) && !cfg.Denied[c] {
					cats = append(cats, "@"+c)
				} else {
					off = append(off, "@"+c)
				}
			}
			sort.Strings(cats)
			sort.Strings(off)
			fmt.Println("settings:", library.Library{Root: client.LibraryDir(e.Home)}.ConfigPath(), "[backup]")
			fmt.Println("categories on: ", strings.Join(cats, " "))
			if len(off) > 0 {
				fmt.Println("categories off:", strings.Join(off, " "))
			}
			rows, err := e.BackupPlan()
			if err != nil {
				return err
			}
			type agg struct {
				n, skipped int
				size       int64
			}
			byCat := map[string]*agg{}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			listed := false
			for _, r := range rows {
				a := byCat[r.Category]
				if a == nil {
					a = &agg{}
					byCat[r.Category] = a
				}
				if r.Selected {
					a.n++
					a.size += r.Size
				} else {
					a.skipped++
				}
				// Individual paths for user-chosen files (and with --all).
				if (r.Category == "files" && r.Selected) || all {
					if !listed {
						fmt.Fprintln(tw, "\nUPLOAD\tCATEGORY\tSIZE\tPATH")
						listed = true
					}
					up := "yes"
					if !r.Selected {
						up = "no"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", up, r.Category, humanize.IBytes(uint64(r.Size)), r.Path)
				}
			}
			tw.Flush()
			var names []string
			for k := range byCat {
				names = append(names, k)
			}
			sort.Strings(names)
			fmt.Println()
			tw = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "CATEGORY\tUPLOADED FILES\tSIZE\tNOT SELECTED")
			for _, k := range names {
				a := byCat[k]
				fmt.Fprintf(tw, "%s\t%d\t%s\t%d\n", k, a.n, humanize.IBytes(uint64(a.size)), a.skipped)
			}
			tw.Flush()
			if n, err := e.CredentialCount(); err == nil {
				state := "off"
				if cfg.Enabled(backup.Credentials) {
					state = "on"
				}
				fmt.Printf("credentials: @credentials %s, %d stored (hearth secret list)\n", state, n)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "list every file, selected or not")
	return c
}
