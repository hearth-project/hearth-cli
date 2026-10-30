package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/library"
)

func libCmd() *cobra.Command {
	c := &cobra.Command{Use: "lib", Short: "Shared skills/agents/instructions library for all agents"}
	var from string
	var overwrite bool
	imp := &cobra.Command{
		Use:   "import codex|claude|comate",
		Short: "Copy an agent's existing skills, agents and instructions into the library",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			t, err := pickTarget(args[0], from)
			if err != nil {
				return err
			}
			home := client.Home()
			acts, err := library.Import(library.Library{Root: client.LibraryDir(home)}, t, client.RenderManifest(home), overwrite)
			printActions(acts)
			if err != nil {
				return err
			}
			var skills []string
			for _, a := range acts {
				if n, ok := strings.CutPrefix(a.Item, "skill:"); ok && a.Op == "import" {
					skills = append(skills, n)
				}
			}
			if added, err := client.AddResident(home, skills...); err != nil {
				return err
			} else if len(added) > 0 {
				fmt.Printf("resident (in the agents' own skill lists): %s\n", strings.Join(added, ", "))
			}
			fmt.Println("next: `hearth lib sync --adopt` to let hearth manage these paths (originals are backed up)")
			return nil
		},
	}
	imp.Flags().StringVar(&from, "from", "", "agent directory to import from (default: $CODEX_HOME / ~/.claude); a project's .codex or .claude also works")
	imp.Flags().BoolVar(&overwrite, "overwrite", false, "replace items already in the library")

	var adopt, dry bool
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Render the library into every agent (skills symlinked, agents converted)",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			if e, err := engine(); err == nil {
				// Logged in: include linked shared items.
				acts, err := e.RenderOpts(library.Options{Adopt: adopt, DryRun: dry})
				printActions(acts)
				return err
			}
			acts, err := library.Render(library.Library{Root: client.LibraryDir(home)}, client.EnabledLibTargets(home),
				library.Options{Manifest: client.RenderManifest(home), BackupDir: client.BackupDir(home), Adopt: adopt, DryRun: dry})
			printActions(acts)
			return err
		},
	}
	sync.Flags().BoolVar(&adopt, "adopt", false, "back up and replace existing unmanaged skills/agents with the library version")
	sync.Flags().BoolVar(&dry, "dry-run", false, "only show what would change")

	var allowSecrets bool
	push := &cobra.Command{
		Use:   "push",
		Short: "Upload the library to your private space on the server",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			r, err := e.LibPush(allowSecrets)
			if err != nil {
				return err
			}
			fmt.Printf("library %s (version %d)\n", r.Op, r.Version)
			return nil
		},
	}
	push.Flags().BoolVar(&allowSecrets, "allow-secrets", false, "upload even if the secret scan finds something")

	var force bool
	pull := &cobra.Command{
		Use:   "pull",
		Short: "Download the library from the server and render it",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			return pullAndRender(e, force)
		},
	}
	pull.Flags().BoolVar(&force, "force", false, "discard unsynced local changes (they are backed up)")

	status := &cobra.Command{
		Use:   "status",
		Short: "List library contents",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			lc, err := library.Library{Root: client.LibraryDir(home)}.Load()
			if err != nil {
				return err
			}
			fmt.Printf("library: %s\n", client.LibraryDir(home))
			resident := map[string]bool{}
			for _, r := range lc.Resident {
				resident[r] = true
			}
			t := &table{}
			t.add("KIND", "NAME", "IN AGENTS", "DESCRIPTION")
			w := termWidth() - 60
			if w < 30 {
				w = 30
			}
			nRes := 0
			for _, s := range lc.Skills {
				in := "on demand"
				if lc.Resident == nil || resident[s.Name] {
					in, nRes = "resident", nRes+1
				}
				b, _ := os.ReadFile(filepath.Join(s.Dir, "SKILL.md"))
				t.add("skill", s.Name, in, truncW(library.SkillDescription(b), w))
			}
			for _, a := range lc.Agents {
				t.add("agent", a.Name, "resident", truncW(a.Description, w))
			}
			if lc.Instructions != "" {
				t.add("instructions", "AGENTS.md", "resident", "")
			}
			t.print()
			fmt.Printf("%d skills (%d resident, the rest found on demand through skill search) and %d agents; change: [skills] resident in hearth config --edit\n",
				len(lc.Skills), nRes, len(lc.Agents))
			if fs := client.LibSecretFiles(home); len(fs) > 0 {
				fmt.Printf("credential files in skills, synced end-to-end encrypted (not in the shared library): %s\n", joinMax(fs, 4))
			}
			if msg, _, ok := client.LibraryError(home); ok {
				fmt.Println("last sync:", msg)
			}
			for _, p := range lc.Problems {
				fmt.Println("problem:", p)
			}
			return nil
		},
	}
	c.AddCommand(imp, sync, push, pull, status, configCmd(), skillsDirCmd(), libSearchCmd(), libStatsCmd(), libUsageCmd(), libResidentCmd(), libMCPInstallCmd())
	return c
}

func pullAndRender(e *client.Engine, force bool) error {
	return pullAndRenderThen(e, force, nil)
}

// pullAndRenderThen runs beforeRender between the pull (which brings the
// synced agent settings) and the render.
func pullAndRenderThen(e *client.Engine, force bool, beforeRender func()) error {
	r, err := e.LibPull(force)
	if err != nil {
		return err
	}
	fmt.Printf("library %s (version %d)\n", r.Op, r.Version)
	if beforeRender != nil {
		beforeRender()
	}
	if r.Op == "empty" {
		return nil
	}
	acts, err := e.Render(false)
	printActions(acts)
	return err
}

func pickTarget(name, from string) (library.Target, error) {
	for _, t := range library.DefaultTargets() {
		if t.Name != name {
			continue
		}
		if from != "" {
			abs, err := filepath.Abs(from)
			if err != nil {
				return t, err
			}
			switch name {
			case "codex":
				return library.CodexTarget(abs), nil
			case "comate":
				return library.ComateTarget(abs), nil
			}
			return library.ClaudeTarget(abs), nil
		}
		return t, nil
	}
	return library.Target{}, fmt.Errorf("unknown agent %q (want codex, claude or comate)", name)
}

// verboseActions prints every rendered item (HEARTH_VERBOSE=1 or -v
// commands); otherwise only warnings, conflicts and a per-agent summary.
var verboseActions = os.Getenv("HEARTH_VERBOSE") == "1"

func printActions(acts []library.Action) {
	n := map[string]int{}
	perAgent := map[string]map[string]int{}
	for _, a := range acts {
		n[a.Op]++
		if a.Op == "unchanged" || a.Op == "present" {
			continue
		}
		agent := agentOfPath(a.Path)
		if strings.HasPrefix(a.Item, "mcp:") {
			agent = "mcp"
		}
		if perAgent[agent] == nil {
			perAgent[agent] = map[string]int{}
		}
		perAgent[agent][a.Op]++
		quiet := a.Op == "create" || a.Op == "update" || a.Op == "remove"
		if quiet && !verboseActions {
			continue
		}
		line := fmt.Sprintf("%-9s %-24s %s", a.Op, a.Item, a.Path)
		if a.Detail != "" {
			line += "  (" + a.Detail + ")"
		}
		fmt.Println(line)
	}
	if n["present"] > 0 {
		fmt.Printf("      %d skills already present in the agents' own dirs (kept, not linked again)\n", n["present"])
	}
	if len(perAgent) == 0 {
		return
	}
	var keys []string
	for k := range perAgent {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var parts []string
		for _, op := range []string{"create", "update", "remove", "warn", "conflict", "skip"} {
			if c := perAgent[k][op]; c > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", c, op))
			}
		}
		if len(parts) > 0 {
			fmt.Printf("      %-8s %s\n", k+":", strings.Join(parts, ", "))
		}
	}
	if !verboseActions && n["create"]+n["update"]+n["remove"] > 0 {
		fmt.Println("      (details: HEARTH_VERBOSE=1 hearth lib sync)")
	}
}

// agentOfPath names the agent directory a rendered path belongs to.
func agentOfPath(p string) string {
	for _, a := range []string{"codex", "claude", "comate"} {
		if strings.Contains(p, "/."+a+"/") || strings.Contains(p, "/."+a+"-") {
			return a
		}
	}
	if p == "" {
		return "library"
	}
	return "other"
}

func trunc(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// skillsDirCmd shows or changes where the single copy of the skills lives.
func skillsDirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "skills-dir [path]",
		Short: "Where the one copy of the skills is stored (default <library>/skills); saved in <library>/config.toml",
		Long: "Sets skills.dir in <library>/config.toml and moves the existing skills there.\n" +
			"Portable paths (${WORKSPACE}/.agents/skills, ~/.agents/skills, an absolute path) are\n" +
			"resolved on every machine, so the setting follows the library to your other machines.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			home := client.Home()
			dir := ""
			if len(a) == 1 {
				dir = a[0]
			}
			got, err := client.SetSkillsDir(home, dir)
			if err != nil {
				return err
			}
			lib := library.Library{Root: client.LibraryDir(home)}
			cfg, _ := lib.LoadConfig()
			if cfg.Skills.Dir == "" {
				cfg.Skills.Dir = "<default>"
			}
			fmt.Printf("skills dir: %s (config: %s)\n", got, cfg.Skills.Dir)
			if dir == "" {
				fmt.Printf("change: hearth lib skills-dir '<path>'   e.g. hearth lib skills-dir '${WORKSPACE}/.agents/skills'\n")
				return nil
			}
			e, err := engine()
			if err != nil {
				return err
			}
			acts, err := e.Render(false)
			printActions(acts)
			return err
		},
	}
}

// configCmd shows the one settings file.
func configCmd() *cobra.Command {
	var edit bool
	c := &cobra.Command{
		Use:   "config",
		Short: "Show the single settings file (skills, agents, backup, tools, MCP, workspaces)",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			lib := library.Library{Root: client.LibraryDir(home)}
			if _, err := client.EnsureUserConfig(home); err != nil {
				return err
			}
			if edit {
				ed := os.Getenv("VISUAL")
				if ed == "" {
					ed = os.Getenv("EDITOR")
				}
				if ed == "" {
					ed = "vi"
				}
				cmd := exec.Command("sh", "-c", ed+` "$1"`, "sh", lib.ConfigPath())
				cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
				return cmd.Run()
			}
			b, err := os.ReadFile(lib.ConfigPath())
			if err != nil {
				return err
			}
			fmt.Printf("# %s\n\n", lib.ConfigPath())
			fmt.Print(string(b))
			return nil
		},
	}
	c.Flags().BoolVar(&edit, "edit", false, "open the file in $EDITOR")
	return c
}
