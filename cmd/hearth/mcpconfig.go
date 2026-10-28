package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

func mcpConfigCmd() *cobra.Command {
	c := &cobra.Command{Use: "mcp-config", Short: "MCP servers for every agent from [mcp.servers] in config.toml (secrets via ${secret:NAME})"}
	var file string
	var overwrite bool
	imp := &cobra.Command{Use: "import comate|claude", Short: "Import an agent's mcpServers; credential env values become secrets", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			path := file
			if path == "" {
				switch a[0] {
				case "comate":
					path = recipe.ExpandHome("~/.comate/mcp.json")
				case "claude":
					path = recipe.ExpandHome("~/.claude.json")
				default:
					return fmt.Errorf("unknown agent %q (codex: register servers in config.toml [mcp.servers] by hand)", a[0])
				}
			}
			added, skipped, err := e.ImportMCP(path, overwrite)
			if err != nil {
				return err
			}
			fmt.Printf("imported %v into %s", added, client.MCPConfigPath(e.Home))
			if len(skipped) > 0 {
				fmt.Printf("; kept existing %v (use --overwrite)", skipped)
			}
			fmt.Println("\ncredential-looking env values were stored as encrypted secrets; run `hearth mcp-config sync` to register the servers everywhere")
			return nil
		}}
	imp.Flags().StringVar(&file, "file", "", "path to an mcpServers JSON file")
	imp.Flags().BoolVar(&overwrite, "overwrite", false, "replace servers already in config.toml")
	var adopt bool
	sync := &cobra.Command{Use: "sync", Short: "Register the config.toml [mcp.servers] in Codex, Claude Code and Comate", RunE: func(*cobra.Command, []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		acts, err := e.RenderMCP(adopt)
		for _, a := range acts {
			line := fmt.Sprintf("%-9s %-7s %s", a.Op, a.Agent, a.Server)
			if a.Detail != "" {
				line += "  (" + a.Detail + ")"
			}
			fmt.Println(line)
		}
		return err
	}}
	sync.Flags().BoolVar(&adopt, "adopt", false, "replace servers with the same name that hearth did not create")
	list := &cobra.Command{Use: "list", Short: "List the MCP servers of config.toml", RunE: func(*cobra.Command, []string) error {
		cfg, _, err := client.LoadMCPConfig(client.Home())
		if err != nil {
			return err
		}
		names := make([]string, 0, len(cfg.Servers))
		for n := range cfg.Servers {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			s := cfg.Servers[n]
			agents := "all"
			if len(s.Agents) > 0 {
				agents = strings.Join(s.Agents, ",")
			}
			fmt.Printf("%-20s %-14s %s %s\n", n, agents, filepath.Base(s.Command), strings.Join(s.Args, " "))
		}
		return nil
	}}
	c.AddCommand(imp, sync, list)
	return c
}
