package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"strings"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/mcpserver"
	"github.com/hearth-project/hearth-cli/internal/search"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "0.1.0-dev"

// skillSources: project skills (cwd) first, then extra dirs, then the
// personal library, so the most local definition wins.
func skillSources(extra []string) []mcpserver.Source {
	var out []mcpserver.Source
	if wd, err := os.Getwd(); err == nil {
		for _, d := range []string{".agents/skills", ".codex/skills", ".claude/skills"} {
			out = append(out, mcpserver.Source{Name: "project", Dir: filepath.Join(wd, d)})
		}
	}
	for _, d := range extra {
		out = append(out, mcpserver.Source{Name: "extra", Dir: d})
	}
	return append(out, mcpserver.Source{Name: "library", Dir: library.Library{Root: client.LibraryDir(client.Home())}.SkillsDir()})
}

func usageLog() string { return filepath.Join(client.Home(), "usage.jsonl") }

func mcpCmd() *cobra.Command {
	var extra []string
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Serve skill search over MCP on stdio (search_skills, get_skill, list_skills)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			os.MkdirAll(client.Home(), 0o700)
			home := client.Home()
			svc := &mcpserver.Service{Sources: skillSources(extra), UsageLog: usageLog(),
				Recipes: func() string { return client.RecipesSummary(home) },
				Propose: func(r, steps, reason string) (string, error) { return client.Propose(home, r, steps, reason) }}
			if client.MemoryOn(home) {
				svc.Recall = func(q, cwd string, limit int) (string, error) {
					hits, err := client.Recall(home, q, cwd, limit)
					if err != nil || len(hits) == 0 {
						return "", err
					}
					return client.FormatHits(hits, 6000) + "\nDetails of one: `hearth recall --show <id>`.", nil
				}
			}
			if e, err := engine(); err == nil {
				if client.MemoryOn(home) {
					svc.Remember = func(t, scope string) (string, error) {
						f, err := e.Remember(t, scope, "agent")
						return "f:" + f.ID, err
					}
					svc.Forget = e.ForgetFact
				}
				// Logged in: also search linked and trusted shared skills.
				svc.Remote, svc.Fetch = e.SearchableSkills, e.FetchSkill
				svc.RemoteSearch = func(q string, limit int) []search.Result { return e.SearchTeams(q, limit, 1500*time.Millisecond) }
				svc.TeamList = func() []search.Skill { return e.TeamSkills(2 * time.Second) }
			}
			return svc.New(version).Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
	c.Flags().StringSliceVar(&extra, "skills-dir", nil, "additional skill directories to index (repeatable)")
	return c
}

func libSearchCmd() *cobra.Command {
	var limit int
	var extra []string
	c := &cobra.Command{
		Use:   "search <query>",
		Short: "Search skills the way the MCP tool does",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			svc := &mcpserver.Service{Sources: skillSources(extra)}
			ix, err := svc.Index()
			if err != nil {
				return err
			}
			q := args[0]
			for _, a := range args[1:] {
				q += " " + a
			}
			n := 0
			for _, r := range ix.Search(q, limit) {
				n++
				fmt.Printf("%d. %-28s %6.2f  %-8s %s\n", n, r.Name, r.Score, r.Source, trunc(r.Description, 70))
			}
			fmt.Printf("(%d skills indexed here)\n", ix.Len())
			// Teams the user is in or can read: searched on the server.
			if e, err := engine(); err == nil {
				for _, r := range e.SearchTeams(q, limit, 3*time.Second) {
					n++
					fmt.Printf("%d. %-28s %6.2f  %s  %s\n", n, r.Name, r.Score, r.Source, trunc(r.Description, 70))
				}
			}
			return nil
		},
	}
	c.Flags().IntVar(&limit, "limit", 5, "results")
	c.Flags().StringSliceVar(&extra, "skills-dir", nil, "additional skill directories to index")
	return c
}

func libStatsCmd() *cobra.Command {
	var server bool
	var days int
	c := &cobra.Command{
		Use:   "stats",
		Short: "Most used skills, last 30 days (MCP get_skill + session logs)",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if server {
				ranks, err := e.ServerUsage(days, 20)
				if err != nil {
					return err
				}
				if len(ranks) == 0 {
					fmt.Println("no usage on the server yet")
					return nil
				}
				for _, r := range ranks {
					fmt.Printf("%5d  %s\n", r.Count, r.Name)
				}
				if ag, err := e.API.UserUsageOf("agent", days, 20); err == nil && len(ag) > 0 {
					fmt.Println("agents:")
					for _, r := range ag {
						fmt.Printf("%5d  %s\n", r.Count, r.Name)
					}
				}
				return nil
			}
			u, err := e.LocalUsage(time.Now().AddDate(0, 0, -days))
			if err != nil {
				return err
			}
			counts := map[string]int64{}
			for _, m := range u {
				for k, n := range m {
					counts[k] += n
				}
			}
			if len(counts) == 0 {
				fmt.Println("no usage recorded yet")
				return nil
			}
			names := make([]string, 0, len(counts))
			for n := range counts {
				names = append(names, n)
			}
			sort.Slice(names, func(i, j int) bool { return counts[names[i]] > counts[names[j]] })
			for _, n := range names {
				fmt.Printf("%5d  %s\n", counts[n], n)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&server, "server", false, "rank across all machines of this account (usage is uploaded first)")
	c.Flags().IntVar(&days, "days", 30, "window in days")
	return c
}

// libResidentCmd sets [skills] resident from the account-wide usage ranking.
func libResidentCmd() *cobra.Command {
	top := 5
	days := 30
	c := &cobra.Command{
		Use:   "resident",
		Short: "Make the most used skills resident (server ranking across your machines)",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			ranks, err := e.ServerUsage(days, top+20)
			if err != nil {
				return err
			}
			lib := library.Library{Root: client.LibraryDir(e.Home)}
			have := map[string]bool{}
			if c, err := lib.Load(); err == nil {
				for _, s := range c.Skills {
					have[s.Name] = true
				}
			}
			var pick []string
			for _, r := range ranks {
				if have[r.Name] && len(pick) < top {
					pick = append(pick, r.Name)
				}
			}
			if len(pick) == 0 {
				return fmt.Errorf("no skill usage on the server yet; use the skills (or run with hearth mcp registered), then retry")
			}
			cfg, err := client.UserConfig(e.Home)
			if err != nil {
				return err
			}
			if cfg.Skills == nil {
				cfg.Skills = &library.SkillsConfig{}
			}
			cfg.Skills.Resident = pick
			if err := client.SaveUserConfig(e.Home, cfg); err != nil {
				return err
			}
			fmt.Printf("resident (%d): %s\n", len(pick), strings.Join(pick, ", "))
			acts, err := e.Render(false)
			printActions(acts)
			return err
		},
	}
	c.Flags().IntVar(&top, "top", 5, "how many skills to keep resident")
	c.Flags().IntVar(&days, "days", 30, "usage window in days")
	return c
}

// libMCPInstallCmd registers `hearth mcp` with each agent through the
// agent's own CLI rather than editing its config files.
func libMCPInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp-install",
		Short: "Register the skill-search MCP server with Codex and Claude Code",
		RunE: func(cmd *cobra.Command, _ []string) error {
			done, err := installSkillMCP(false)
			if err != nil {
				return err
			}
			if len(done) == 0 {
				fmt.Println("no Codex or Claude Code CLI found; nothing to register")
			}
			return nil
		},
	}
}

// installSkillMCP registers `hearth mcp` with the agents whose CLI exists.
// Idempotent: an existing "hearth" server is left as it is (the binary may
// have moved, e.g. after reinstall, so the command is refreshed).
func installSkillMCP(quiet bool) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	env := "HEARTH_HOME=" + client.Home()
	type ag struct {
		name    string
		add     []string
		refresh []string
	}
	agents := []ag{
		{"codex", []string{"mcp", "add", "--env", env, "hearth", "--", exe, "mcp"}, []string{"mcp", "remove", "hearth"}},
		// add-json: claude's -e takes several values and swallowed the
		// server name after it ("Invalid environment variable format: hearth").
		{"claude", []string{"mcp", "add-json", "--scope", "user", "hearth", claudeMCPJSON(exe)}, []string{"mcp", "remove", "hearth", "--scope", "user"}},
	}
	var done []string
	for _, a := range agents {
		bin, err := exec.LookPath(a.name)
		if err != nil {
			if !quiet {
				fmt.Printf("%-6s not installed, skipped\n", a.name)
			}
			continue
		}
		listed, _ := exec.CommandContext(context.Background(), bin, "mcp", "list").CombinedOutput()
		registered := strings.Contains(string(listed), "hearth")
		if registered {
			// Re-add so the command points at this binary and HEARTH_HOME.
			exec.CommandContext(context.Background(), bin, a.refresh...).Run()
		}
		out, err := exec.CommandContext(context.Background(), bin, a.add...).CombinedOutput()
		if err != nil {
			if !quiet {
				fmt.Printf("%-6s failed: %v\n%s  by hand: %s %s\n", a.name, err, out, a.name, shellJoin(a.add))
			}
			continue
		}
		done = append(done, a.name)
		if !quiet {
			fmt.Printf("%-6s registered MCP server \"hearth\"\n", a.name)
		}
	}
	return done, nil
}

// libUsageCmd manages the account's skill usage counts on the server.
func libUsageCmd() *cobra.Command {
	c := &cobra.Command{Use: "usage", Short: "Skill usage counts on the server (ranking: hearth lib stats --server)"}
	var yes, dry bool
	prune := &cobra.Command{
		Use:   "prune [skill ...]",
		Short: "Delete the usage counts of deleted, renamed or test skills (default: every skill that no longer exists here)",
		Long: "Without names, lists the skills with usage counts on the server that are not in the library, not linked and\n" +
			"not installed in any agent on this machine, and deletes their counts after a yes. With names, deletes those.\n" +
			"Machines no longer upload counts of skills they do not have, so pruned names do not come back.",
		RunE: func(_ *cobra.Command, args []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			var names []string
			if len(args) > 0 {
				names = args
			} else {
				stale, err := e.StaleUsage()
				if err != nil {
					return err
				}
				if len(stale) == 0 {
					fmt.Println("nothing to prune: every skill with usage counts still exists")
					return nil
				}
				t := &table{}
				t.add("COUNT", "SKILL")
				for _, r := range stale {
					t.add(strconv.FormatInt(r.Count, 10), r.Name)
					names = append(names, r.Name)
				}
				fmt.Printf("%d skill(s) with usage counts that no longer exist here (last 365 days):\n", len(stale))
				t.print()
			}
			if dry {
				return nil
			}
			if !yes {
				if !isTTY() {
					return fmt.Errorf("re-run with --yes to delete the counts of %d skill(s)", len(names))
				}
				a, ok := askLine(fmt.Sprintf("Delete the usage counts of these %d skill(s)? [y/N] ", len(names)))
				if !ok || !strings.EqualFold(a, "y") && !strings.EqualFold(a, "yes") {
					fmt.Println("nothing deleted")
					return nil
				}
			}
			n, err := e.API.PruneUserUsage(names)
			if err != nil {
				return err
			}
			fmt.Printf("deleted %d daily count(s) of %d skill(s)\n", n, len(names))
			return nil
		},
	}
	prune.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask")
	prune.Flags().BoolVar(&dry, "dry-run", false, "only list what would be deleted")
	c.AddCommand(prune)
	return c
}

func claudeMCPJSON(exe string) string {
	b, _ := json.Marshal(map[string]any{"type": "stdio", "command": exe, "args": []string{"mcp"}, "env": map[string]string{"HEARTH_HOME": client.Home()}})
	return string(b)
}

// shellJoin quotes args for a command the user can paste.
func shellJoin(args []string) string {
	var out []string
	for _, a := range args {
		if a != "" && !strings.ContainsAny(a, " '\"{}$\\*?;&|<>()") {
			out = append(out, a)
			continue
		}
		out = append(out, "'"+strings.ReplaceAll(a, "'", `'"'"'`)+"'")
	}
	return strings.Join(out, " ")
}
