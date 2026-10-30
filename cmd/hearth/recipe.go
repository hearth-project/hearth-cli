package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

func printResults(res []recipe.Result) (bad, pending, approval int) {
	warns := 0
	for _, r := range res {
		line := fmt.Sprintf("%-15s %-16s %s", r.Op, r.Recipe, r.Step)
		if r.Detail != "" && r.Op != "ok" {
			line += "\n                " + r.Detail
		}
		if r.Op == "pending" && r.Key != "" {
			line += "\n                already done? hearth recipe done " + r.Key
		}
		fmt.Println(line)
		switch r.Op {
		case "warn":
			warns++
		case "failed":
			bad++
		case "pending":
			pending++
		case "needs-approval":
			approval++
		}
	}
	if approval > 0 {
		fmt.Println("\nsteps above run commands from your synced library; approve them with: hearth recipe apply (asks once) or --yes")
	}
	if pending > 0 {
		fmt.Printf("%d manual step(s) still to do (listed as pending); done already: hearth recipe done (asks), or the id shown\n", pending)
	}
	if warns > 0 {
		fmt.Printf("%d warning(s) above (not failures)\n", warns)
	}
	return
}

func runRecipes(mode recipe.Mode, only []string, yes, dry, all, onlyDue bool) error {
	e, err := engine()
	if err != nil {
		return err
	}
	_, _ = e.SiteSync() // the admin's defaults may have changed; offline is fine
	if mode == recipe.Apply && !yes && !dry && isTTY() {
		yes = askApproval(e, only)
	}
	res, err := e.RunRecipes(only, recipe.Options{Mode: mode, Approve: yes, DryRun: dry, Optional: all, OnlyDue: onlyDue})
	bad, _, _ := printResults(res)
	if err != nil {
		return err
	}
	if bad > 0 && mode != recipe.Save {
		return fmt.Errorf("%d step(s) failed", bad)
	}
	return nil
}

// askApproval lists the recipe commands not yet approved (here or on
// another of the user's machines) and asks once for all of them.
func askApproval(e *client.Engine, only []string) bool {
	res, err := e.RunRecipes(only, recipe.Options{Mode: recipe.Apply, DryRun: true})
	if err != nil {
		return false
	}
	var need []recipe.Result
	for _, r := range res {
		if r.Op == "needs-approval" {
			need = append(need, r)
		}
	}
	if len(need) == 0 {
		return false
	}
	fmt.Printf("These restore steps run commands from your synced library (not yet approved on any of your machines):\n")
	for i, r := range need {
		fmt.Printf("  %d. %s / %s\n     %s\n", i+1, r.Recipe, r.Step, truncW(r.Detail, termWidth()-6))
	}
	a, ok := askLine(fmt.Sprintf("Run these %d command(s)? Approving once covers your other machines too. [Y/n] ", len(need)))
	return ok && (a == "" || strings.EqualFold(a, "y") || strings.EqualFold(a, "yes"))
}

func recipeCmd() *cobra.Command {
	c := &cobra.Command{Use: "recipe", Short: "Restore recipes: files, env, commands, checks and manual steps (library/recipes)"}
	var only []string
	var yes, dry bool
	apply := &cobra.Command{Use: "apply", Short: "Run the recipes' commands yourself (setup only restores their files; agents go through hearth recipe plan)", RunE: func(*cobra.Command, []string) error {
		return runRecipes(recipe.Apply, only, yes, dry, false, false)
	}}
	apply.Flags().StringSliceVar(&only, "only", nil, "recipe names")
	apply.Flags().BoolVarP(&yes, "yes", "y", false, "approve the commands shown as needs-approval")
	apply.Flags().BoolVar(&dry, "dry-run", false, "only show what would change")
	c.AddCommand(apply, recipePlanCmd(), recipeRunCmd(), recipeDoneCmd(), proposalsCmd(),
		&cobra.Command{Use: "list", Short: "List recipes", RunE: func(*cobra.Command, []string) error {
			rs, err := client.LoadRecipes(client.Home(), nil)
			for _, r := range rs {
				fmt.Printf("%-24s %2d steps  %s\n", r.Name, len(r.Steps), r.Description)
			}
			return err
		}},
		&cobra.Command{Use: "show <name>", Short: "Print a recipe", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			b, err := os.ReadFile(filepath.Join(client.RecipesDir(client.Home()), a[0]+".toml"))
			if err == nil {
				os.Stdout.Write(b)
			}
			return err
		}},
		&cobra.Command{Use: "add <file.toml>", Short: "Validate a recipe and copy it into the library", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			b, err := os.ReadFile(a[0])
			if err != nil {
				return err
			}
			r, err := recipe.Parse(b)
			if err != nil {
				return err
			}
			dst := filepath.Join(client.RecipesDir(client.Home()), r.Name+".toml")
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				return err
			}
			fmt.Printf("added recipe %s (%d steps); it syncs with your library. Referenced library files go under %s\n",
				r.Name, len(r.Steps), filepath.Join(client.LibraryDir(client.Home()), "files"))
			return nil
		}},
		&cobra.Command{Use: "rm <name>", Short: "Remove a recipe", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			return report(os.Remove(filepath.Join(client.RecipesDir(client.Home()), a[0]+".toml")), "removed recipe "+a[0])
		}},
	)
	return c
}

func doctorCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{Use: "doctor", Short: "Run recipe checks and show pending manual steps (changes nothing)", RunE: func(*cobra.Command, []string) error {
		home := client.Home()
		fmt.Printf("hearth home: %s\n", home)
		if pid := client.DaemonPID(home); pid != 0 {
			fmt.Printf("daemon:  running (pid %d)\n", pid)
		} else {
			fmt.Println("daemon:  not running (hearth daemon start)")
		}
		printMachine(home, "")
		// The agents' own setup: login files where Codex really looks,
		// app-servers left on an old home, gateway route.
		if e, err := engine(); err == nil {
			client.FreshChecks = true
			if h := e.MachineHealth(); len(h.Issues) == 0 {
				fmt.Println("environment: ok")
			} else {
				fmt.Println("environment:")
				for _, is := range h.Issues {
					fmt.Println("  -", is)
				}
			}
		}
		if msg, when, ok := client.LibraryError(home); ok {
			fmt.Printf("library: last sync failed %s: %s\n", when.Format("2006-01-02 15:04"), msg)
			fmt.Println("         recipes and credentials in it are stale on this machine; run: hearth lib pull --force (local copy is backed up)")
		}
		learnHints(home)
		return runRecipes(recipe.Doctor, nil, false, false, all, false)
	}}
	c.Flags().BoolVar(&all, "all", false, "also run optional checks")
	return c
}

func upCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{Use: "up", Short: "After a container restart: start the sync daemon and recipe services", RunE: func(*cobra.Command, []string) error {
		if _, err := client.LoadConfig(client.Home()); err != nil {
			return err
		}
		if client.DaemonPID(client.Home()) == 0 {
			if err := startDaemon(); err != nil {
				return err
			}
		} else {
			fmt.Println("daemon already running")
		}
		return runRecipes(recipe.Up, nil, yes, false, false, false)
	}}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "approve service start commands")
	return c
}

func saveCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{Use: "save", Short: "Run every capture step now (the daemon also runs them on schedule)", RunE: func(*cobra.Command, []string) error {
		return runRecipes(recipe.Save, nil, yes, false, false, false)
	}}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "approve capture commands")
	return c
}

func envCmd() *cobra.Command {
	rc := recipe.DefaultRCFile
	c := &cobra.Command{Use: "env", Short: "The shell activation installed by env steps"}
	c.AddCommand(
		&cobra.Command{Use: "status", Short: "Show the generated script and rc hook", RunE: func(*cobra.Command, []string) error {
			script := recipe.EnvScript(client.Home())
			b, err := os.ReadFile(script)
			if err != nil {
				fmt.Println("no env script yet (no env steps applied)")
				return nil
			}
			rcb, _ := os.ReadFile(rc())
			fmt.Printf("script: %s\nhooked in %s: %v\n\n%s", script, rc(), strings.Contains(string(rcb), script), b)
			return nil
		}},
		&cobra.Command{Use: "uninstall", Short: "Remove the managed block from ~/.bashrc and ~/.zshrc", RunE: func(*cobra.Command, []string) error {
			removed := false
			for _, f := range []string{recipe.ExpandHome("~/.bashrc"), recipe.ExpandHome("~/.zshrc")} {
				changed, err := recipe.UninstallRC(f, recipe.EnvScript(client.Home()))
				if err != nil {
					return err
				}
				if changed {
					fmt.Println("removed the hearth block from " + f)
					removed = true
				}
			}
			if !removed {
				fmt.Println("nothing to remove")
			}
			return nil
		}},
	)
	return c
}

// recipeDoneCmd marks restore steps done on this machine (they are then
// no longer listed as to do here; other machines still list them).
func recipeDoneCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{Use: "done [step-key ...]", Short: "Mark restore steps as done on this machine (keys from hearth recipe plan; none: pick)",
		RunE: func(_ *cobra.Command, ids []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			plan, err := e.Plan()
			if err != nil {
				return err
			}
			var todo []recipe.PlanStep
			for _, p := range plan {
				if p.Status == "todo" {
					todo = append(todo, p)
				}
			}
			var mark []string
			switch {
			case all:
				for _, p := range todo {
					mark = append(mark, p.Key)
				}
			case len(ids) > 0:
				known := map[string]bool{}
				for _, p := range plan {
					known[p.Key] = true
				}
				for _, id := range ids {
					if !known[id] {
						return fmt.Errorf("no step %q in this machine's plan (hearth recipe plan)", id)
					}
					mark = append(mark, id)
				}
			default:
				if len(todo) == 0 {
					fmt.Println("nothing left to do on this machine")
					return nil
				}
				for i, p := range todo {
					fmt.Printf("  %d. %s / %s\n     %s\n", i+1, p.Recipe, planLabel(p), truncW(p.Do, termWidth()-6))
				}
				if !isTTY() {
					return fmt.Errorf("pass the keys to mark (or --all)")
				}
				a, ok := askLine("Which ones are done here? e.g. 1 2 · a = all · Enter = none: ")
				if !ok || a == "" {
					return nil
				}
				idx, mode, err := parsePick(a, len(todo))
				if err != nil {
					return err
				}
				if mode == "all" {
					for i := range todo {
						idx = append(idx, i)
					}
				}
				for _, i := range idx {
					mark = append(mark, todo[i].Key)
				}
			}
			if err := e.MarkPlanDone(mark...); err != nil {
				return err
			}
			fmt.Printf("marked %d step(s) done on this machine\n", len(mark))
			reportHealthQuiet(e) // the console and other machines see it now, not in 5 minutes
			return nil
		}}
	c.Flags().BoolVar(&all, "all", false, "mark every step still to do")
	return c
}

// recipeRunCmd runs chosen plan steps with Hearth's own installer (the
// agent uses it after the user agreed; it may also do a step by hand).
func recipeRunCmd() *cobra.Command {
	return &cobra.Command{Use: "run <step-key> ...", Short: "Run steps of this machine's plan now (the user has agreed to them) and mark the ones that worked done",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, keys []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			only := map[string]bool{}
			for _, k := range keys {
				only[k] = true
			}
			res, err := e.RunRecipes(nil, recipe.Options{Mode: recipe.Apply, Approve: true, OnlyKeys: only})
			if err != nil {
				return err
			}
			if len(res) == 0 {
				return fmt.Errorf("no such step on this machine (keys from hearth recipe plan)")
			}
			bad, _, _ := printResults(res)
			var done []string
			for _, r := range res {
				if (r.Op == "ok" || r.Op == "applied") && !strings.Contains(r.Step, ":service") {
					done = append(done, r.Key)
				}
			}
			if len(done) > 0 {
				if err := e.MarkPlanDone(done...); err != nil {
					return err
				}
			}
			reportHealthQuiet(e)
			if bad > 0 {
				return fmt.Errorf("%d step(s) failed", bad)
			}
			return nil
		}}
}

func planLabel(p recipe.PlanStep) string {
	if p.Name != "" {
		return p.Type + " " + p.Name
	}
	return p.Type
}

// recipePlanCmd prints this machine's restore plan for the agent (or the
// user) to work through; nothing is run.
func recipePlanCmd() *cobra.Command {
	var asJSON, all bool
	c := &cobra.Command{Use: "plan", Short: "This machine's restore plan: software and commands to set up, with checks (the hearth skill works through it)",
		Long: "Lists the steps of your recipes (and the server's site recipes) that install software or run commands on this\n" +
			"machine, in order, with a check command for each. Hearth does not run them on setup: ask your agent \"用 hearth 恢复这台\n" +
			"机器的环境\" and it goes through them with you (how to install each tool: hearth docs env). Mark a step done here with\n" +
			"hearth recipe done <key>.",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			_, _ = e.SiteSync()
			plan, err := e.Plan()
			if err != nil {
				return err
			}
			var show []recipe.PlanStep
			for _, p := range plan {
				if all || p.Status == "todo" {
					show = append(show, p)
				}
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				m, _ := client.Machine(e.Home)
				return enc.Encode(map[string]any{"machine": m.Facts, "profiles": m.Names(), "steps": show,
					"guide": "hearth docs env", "run": "hearth recipe run <key>", "mark_done": "hearth recipe done <key>"})
			}
			if len(show) == 0 {
				fmt.Println("nothing left to do on this machine (hearth recipe plan --all lists done and skipped steps)")
				return nil
			}
			for i, p := range show {
				st := ""
				if p.Status != "todo" {
					st = " [" + p.Status + "]"
				}
				fmt.Printf("%d. %s / %s%s\n   %s\n", i+1, p.Recipe, planLabel(p), st, p.Do)
				for _, c := range p.Commands {
					fmt.Printf("   $ %s\n", c)
				}
				if p.Check != "" {
					fmt.Printf("   check: %s\n", p.Check)
				}
				if p.Interactive {
					fmt.Println("   (interactive: run it in your own terminal)")
				}
				if p.Why != "" {
					fmt.Printf("   (%s)\n", p.Why)
				}
				if p.Type == "manual" {
					fmt.Printf("   done: hearth recipe done %s\n", p.Key)
				} else {
					fmt.Printf("   run with Hearth: hearth recipe run %s · done by hand: hearth recipe done %s\n", p.Key, p.Key)
				}
			}
			fmt.Println("\nask your agent \"用 hearth 恢复这台机器的环境\" to go through these; how to install each tool: hearth docs env")
			return nil
		}}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON for agents")
	c.Flags().BoolVar(&all, "all", false, "also steps done or skipped on this machine")
	return c
}
