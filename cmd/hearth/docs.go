package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	hdocs "github.com/hearth-project/hearth-cli/docs"
	"github.com/hearth-project/hearth-cli/internal/client"
)

// docsCmd prints the command reference as Markdown (docs/cli.md is
// generated with `hearth docs cli > docs/cli.md`).
func docsCmd() *cobra.Command {
	c := &cobra.Command{Use: "docs", Short: "Documentation: how to install the agents' software (env), the command reference (cli)"}
	var en bool
	env := &cobra.Command{
		Use:   "env",
		Short: "How to install and check Node, Codex CLI, Claude Code and codex-mixin, plus your organisation's notes from the server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			guide := hdocs.ClientEnvZH
			if en {
				guide = hdocs.ClientEnvEN
			}
			fmt.Fprint(out, guide)
			// The organisation's part (mirrors, proxies, gateway presets),
			// published in the server's site directory.
			home := client.Home()
			_, _ = client.SiteSyncFrom(home, client.DefaultServer(home))
			for _, name := range []string{"client-env.md", "client-env.en.md"} {
				if (name == "client-env.en.md") != en {
					continue
				}
				if b, err := os.ReadFile(filepath.Join(client.SiteDir(home), "files", name)); err == nil {
					fmt.Fprintf(out, "\n\n---\n\n%s", b)
				}
			}
			return nil
		},
	}
	env.Flags().BoolVar(&en, "en", false, "English")
	c.AddCommand(env)
	c.AddCommand(&cobra.Command{
		Use:   "cli",
		Short: "Print the command reference as Markdown",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := cmd.Root()
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "# Hearth command reference")
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Generated from `hearth --help` by `hearth docs cli > docs/cli.md`; every command also has `--help`.")
			fmt.Fprintln(out)
			fmt.Fprintln(out, "| Command | Description |")
			fmt.Fprintln(out, "|---|---|")
			walk(root, func(c *cobra.Command) {
				if c != root {
					fmt.Fprintf(out, "| [`%s`](#%s) | %s |\n", c.CommandPath(), anchor(c), strings.ReplaceAll(c.Short, "|", "\\|"))
				}
			})
			walk(root, func(c *cobra.Command) {
				if c != root {
					writeCmd(out, c)
				}
			})
			return nil
		},
	})
	return c
}

func walk(c *cobra.Command, f func(*cobra.Command)) {
	if (c.Hidden && c.Annotations["documented"] == "") || c.Name() == "help" || c.Name() == "completion" {
		return
	}
	f(c)
	for _, s := range c.Commands() {
		walk(s, f)
	}
}

func anchor(c *cobra.Command) string {
	return strings.ReplaceAll(strings.ToLower(c.CommandPath()), " ", "-")
}

func writeCmd(out io.Writer, c *cobra.Command) {
	fmt.Fprintf(out, "\n## %s\n\n", c.CommandPath())
	desc := c.Long
	if desc == "" {
		desc = c.Short
	}
	fmt.Fprintln(out, desc)
	fmt.Fprintf(out, "\n```text\n%s\n", c.UseLine())
	if fl := c.LocalFlags().FlagUsages(); fl != "" {
		fmt.Fprintf(out, "\nFlags:\n%s", fl)
	}
	fmt.Fprintln(out, "```")
}
