package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// askLine prints a prompt on stderr and reads one line from stdin.
func askLine(prompt string) (string, bool) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := stdinLines.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// nextUserStep returns the next free "user-N" number in r.
func nextUserStep(r *recipe.Recipe) int {
	n := 0
	for _, s := range r.Steps {
		if v, ok := strings.CutPrefix(s.Name, "user-"); ok {
			if i, err := strconv.Atoi(v); err == nil && i > n {
				n = i
			}
		}
	}
	return n + 1
}

func learnCmd() *cobra.Command {
	var yesAll, list, noAsk, noPush, all, asJSON, oneByOne, later bool
	var autoCreds string
	var acceptIDs, hereIDs, neverIDs, steps, hereSteps []string
	c := &cobra.Command{
		Use:   "learn",
		Short: "Discover this user's credentials, configs and setup steps and record them in the synced recipe \"learned\"",
		Long: "Inspects the workspace, home and environment (SSH keys, agent logins, git/gh credentials, tokens, env vars,\n" +
			"agent CLIs, codex-mixin, MCP servers, repos) and asks what a new machine should get back. Answers are kept\n" +
			"permanently on the server: files become encrypted secrets, the rest steps of library/recipes/learned.toml.\n" +
			"\"never\" stops asking about an item on every machine. Run it again whenever your setup changes.",
		RunE: func(*cobra.Command, []string) error {
			if autoCreds != "" {
				on := autoCreds == "true" || autoCreds == "on"
				if !on && autoCreds != "false" && autoCreds != "off" {
					return fmt.Errorf("--auto-credentials wants true or false")
				}
				if err := client.SetAutoCredentials(client.Home(), on); err != nil {
					return err
				}
				fmt.Printf("automatic recording of credentials: %v (synced to all your machines)\n", on)
				return nil
			}
			if later {
				if err := client.SnoozeLearn(client.Home(), 30*24*time.Hour); err != nil {
					return err
				}
				fmt.Println("hearth status will not remind you about unrecorded items for 30 days")
				return nil
			}
			e, err := engine()
			if err != nil {
				return err
			}
			home := e.Home
			ignored := client.Ignored(home)
			learned, err := client.LoadLearned(home)
			if err != nil {
				return err
			}
			var found []client.Finding
			for _, f := range e.Discover() {
				if !ignored[f.ID] {
					found = append(found, f)
				}
			}
			status := e.LearnStatus(found, learned)
			var todo []client.Finding
			known := 0
			for _, f := range found {
				if status[f.ID] == "known" && !all {
					known++
					continue
				}
				todo = append(todo, f)
			}
			if list && asJSON {
				type item struct {
					ID         string `json:"id"`
					Kind       string `json:"kind"`
					Title      string `json:"title"`
					Detail     string `json:"detail"`
					Status     string `json:"status"`
					PerMachine bool   `json:"per_machine"`
				}
				out := []item{}
				for _, f := range found {
					out = append(out, item{f.ID, f.Kind, f.Title, f.Detail, status[f.ID], f.PerMachine})
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"findings": out, "ignored": len(ignored)})
			}
			if list {
				for _, f := range found {
					fmt.Printf("%-8s %-11s %-40s %s\n", status[f.ID], f.Kind, f.ID, f.Detail)
				}
				fmt.Printf("%d finding(s), %d already learned, %d ignored\n", len(found), known, len(ignored))
				return nil
			}
			// Decisions given on the command line (agents, scripts): no prompts.
			decisions := map[string]string{}
			for _, id := range acceptIDs {
				if id == "all" {
					for _, f := range todo {
						decisions[f.ID] = "a"
					}
					continue
				}
				decisions[id] = "a"
			}
			for _, id := range hereIDs {
				decisions[id] = "m"
			}
			for _, id := range neverIDs {
				decisions[id] = "v"
			}
			batch := len(decisions) > 0 || len(steps) > 0 || len(hereSteps) > 0
			if batch {
				byID := map[string]bool{}
				for _, f := range found {
					byID[f.ID] = true
				}
				for id := range decisions {
					if !byID[id] {
						return fmt.Errorf("no finding %q (see hearth learn --list)", id)
					}
				}
				todo = nil
				for _, f := range found {
					if _, ok := decisions[f.ID]; ok {
						todo = append(todo, f)
					}
				}
				noAsk = true
			}
			if known > 0 && !batch {
				fmt.Fprintf(os.Stderr, "%d item(s) already learned and unchanged (--all to review them)\n", known)
			}
			// Interactive: one checklist with a proposal per item instead of
			// a question per item.
			if !batch && !yesAll && !oneByOne && len(todo) > 0 {
				d, ok := learnChecklist(todo, status)
				if !ok {
					fmt.Println("nothing recorded")
					return nil
				}
				decisions, batch = d, true
			}
			before := client.StepTOML(learned.Steps)
			accepted := 0
			// Items can be kept for every machine (a habit) or only for this
			// kind of machine (its profile, created on first use).
			profile := ""
			here := func() (string, error) {
				if profile == "" {
					n, created, err := client.EnsureHereProfile(home)
					if err != nil {
						return "", err
					}
					if created {
						fmt.Printf("  created machine profile %q for machines like this one (hearth machine)\n", n)
					}
					profile = n
				}
				return profile, nil
			}
			for i, f := range todo {
				ans := "y"
				if batch {
					ans = decisions[f.ID]
				} else if !yesAll {
					tag := ""
					if status[f.ID] == "changed" {
						tag = " (changed since last learned: update the stored copy?)"
					}
					hint := "[a]ll machines / [m] only machines like this / [n]o / ne[v]er ask again: "
					if f.PerMachine {
						hint = "[m] only machines like this (looks machine-specific) / [a]ll machines / [n]o / ne[v]er: "
					}
					fmt.Fprintf(os.Stderr, "\n[%d/%d] %s: %s%s\n      %s\n", i+1, len(todo), f.Kind, f.Title, tag, f.Detail)
					a, ok := askLine("      keep it? " + hint)
					if !ok {
						noAsk = true
						break
					}
					ans = strings.ToLower(a)
				}
				switch ans {
				case "m", "machine", "here":
					p, err := here()
					if err != nil {
						return err
					}
					did, err := e.AcceptFor(f, learned, p)
					if err != nil {
						fmt.Fprintf(os.Stderr, "      failed: %v\n", err)
						continue
					}
					accepted++
					fmt.Printf("  + %s (profile %s): %s\n", f.ID, p, did)
				case "y", "yes", "a", "all":
					did, err := e.Accept(f, learned)
					if err != nil {
						fmt.Fprintf(os.Stderr, "      failed: %v\n", err)
						continue
					}
					accepted++
					fmt.Printf("  + %s: %s\n", f.ID, did)
				case "v", "never":
					if err := client.IgnoreFinding(home, f.ID); err != nil {
						return err
					}
					fmt.Printf("  - %s: will not ask again\n", f.ID)
				}
			}
			if !noAsk && !yesAll && (oneByOne || len(acceptIDs)+len(hereIDs)+len(neverIDs)+len(steps)+len(hereSteps) == 0) {
				fmt.Fprintln(os.Stderr, "\nAnything else you always do on a new machine? \"$ command\" runs a command, other text is a reminder,")
				fmt.Fprintln(os.Stderr, "\"here:\" first = only machines like this one. Enter = done.")
				n := nextUserStep(learned)
				for {
					a, ok := askLine("> ")
					if !ok || a == "" {
						break
					}
					rest, scoped := strings.CutPrefix(a, "here:")
					if s, ok := client.UserStep(rest, n); ok {
						steps := []recipe.Step{s}
						if scoped {
							p, err := here()
							if err != nil {
								return err
							}
							steps = client.ScopeSteps(steps, p)
						}
						client.MergeSteps(learned, steps)
						fmt.Printf("  + %s (%s)\n", steps[0].Name, s.Type)
						n++
					}
				}
			}
			if batch {
				n := nextUserStep(learned)
				for i, list := range [][]string{steps, hereSteps} {
					for _, a := range list {
						s, ok := client.UserStep(a, n)
						if !ok {
							continue
						}
						st := []recipe.Step{s}
						if i == 1 {
							p, err := here()
							if err != nil {
								return err
							}
							st = client.ScopeSteps(st, p)
						}
						client.MergeSteps(learned, st)
						fmt.Printf("  + %s (%s)\n", st[0].Name, s.Type)
						n++
					}
				}
			}
			if client.StepTOML(learned.Steps) != before {
				if err := client.SaveRecipe(home, learned); err != nil {
					return err
				}
				fmt.Printf("recipe %q now has %d step(s)\n", client.LearnedRecipe, len(learned.Steps))
			} else if accepted == 0 {
				fmt.Println("nothing new learned")
			}
			if !noPush {
				if r, err := e.LibPush(false); err != nil {
					fmt.Fprintf(os.Stderr, "library not pushed: %v (run: hearth lib push)\n", err)
				} else {
					fmt.Printf("library %s (v%d)\n", r.Op, r.Version)
				}
			}
			if saved, err := e.SaveShell(); err != nil {
				fmt.Fprintf(os.Stderr, "shell: %v\n", err)
			} else {
				for _, s := range saved {
					fmt.Printf("shell: %s saved (%d paragraph(s), encrypted)\n", s.File.Path, s.Paragraphs)
				}
			}
			fmt.Println("review with: hearth recipe show learned; on a new machine: hearth setup (or recipe apply --dry-run)")
			return nil
		},
	}
	c.Flags().BoolVar(&yesAll, "yes-all", false, "accept every finding without asking")
	c.Flags().BoolVar(&oneByOne, "one-by-one", false, "ask about each finding in turn instead of one checklist")
	c.Flags().StringVar(&autoCreds, "auto-credentials", "", "true / false: let the background sync record new credentials by itself (encrypted)")
	c.Flags().BoolVar(&later, "later", false, "do not remind me in hearth status for 30 days")
	c.Flags().BoolVar(&list, "list", false, "only list findings")
	c.Flags().BoolVar(&asJSON, "json", false, "with --list: print JSON (id, kind, title, detail, status, per_machine)")
	c.Flags().StringSliceVar(&acceptIDs, "accept", nil, "keep these finding IDs for all machines (\"all\": every new or changed one); no prompts")
	c.Flags().StringSliceVar(&hereIDs, "here", nil, "keep these finding IDs only for machines like this one (its profile)")
	c.Flags().StringSliceVar(&neverIDs, "never", nil, "never ask about these finding IDs again")
	c.Flags().StringArrayVar(&steps, "step", nil, "add a step for all machines: \"$ command\" runs a command, other text is a reminder (repeatable)")
	c.Flags().StringArrayVar(&hereSteps, "here-step", nil, "add a step only for machines like this one (repeatable)")
	c.Flags().BoolVar(&all, "all", false, "also ask about items already learned and unchanged")
	c.Flags().BoolVar(&noPush, "no-push", false, "do not push the library afterwards")
	return c
}

func proposalsCmd() *cobra.Command {
	c := &cobra.Command{Use: "proposals", Short: "Setup steps proposed by agents (MCP propose_setup_step), waiting for review",
		RunE: func(*cobra.Command, []string) error {
			ps, err := client.Proposals(client.Home())
			if err != nil {
				return err
			}
			if len(ps) == 0 {
				fmt.Println("no pending proposals")
			}
			for _, p := range ps {
				fmt.Printf("## %s  recipe=%s  %s\n%s\n", p.ID, p.Recipe, p.Reason, client.StepTOML(p.Steps))
			}
			return nil
		}}
	resolve := func(accept bool) func(*cobra.Command, []string) error {
		return func(_ *cobra.Command, a []string) error {
			for _, id := range a {
				r, err := client.ResolveProposal(client.Home(), id, accept)
				if err != nil {
					return err
				}
				if accept {
					fmt.Printf("accepted %s into recipe %s\n", id, r)
				} else {
					fmt.Printf("rejected %s\n", id)
				}
			}
			return nil
		}
	}
	c.AddCommand(
		&cobra.Command{Use: "accept <id>...", Short: "Merge proposals into their recipes", Args: cobra.MinimumNArgs(1), RunE: resolve(true)},
		&cobra.Command{Use: "reject <id>...", Short: "Drop proposals", Args: cobra.MinimumNArgs(1), RunE: resolve(false)},
	)
	return c
}

// learnHints tells the user about pending proposals or a missing learned recipe.
func learnHints(home string) {
	if ps, _ := client.Proposals(home); len(ps) > 0 {
		fmt.Printf("%d setup step(s) proposed by agents: hearth recipe proposals\n", len(ps))
	}
	if _, err := os.Stat(client.RecipesDir(home) + "/" + client.LearnedRecipe + ".toml"); os.IsNotExist(err) {
		fmt.Println("tip: run `hearth learn` so new machines get this machine's logins and setup back")
	}
}

// learnChecklist shows every finding with a proposed answer (all machines,
// or only machines like this one when it looks machine-specific) and lets
// the user change some before confirming once. Returns finding id ->
// a (all), m (this kind of machine), n (skip) or v (never ask again).
func learnChecklist(todo []client.Finding, status map[string]string) (map[string]string, bool) {
	dec := map[string]string{}
	for _, f := range todo {
		dec[f.ID] = "a"
		if f.PerMachine {
			dec[f.ID] = "m"
		}
	}
	label := map[string]string{"a": "all machines", "m": "machines like this", "n": "skip", "v": "never ask"}
	home := client.Home()
	show := func() {
		t := &table{}
		t.add("#", "KEEP FOR", "KIND", "ITEM")
		for i, f := range todo {
			item := f.Title + ": " + f.Detail
			if status[f.ID] == "changed" {
				item = "(changed) " + item
			}
			t.add(fmt.Sprint(i+1), label[dec[f.ID]], f.Kind, item)
		}
		t.fit()
		t.print()
	}
	fmt.Printf("Found %d thing(s) on this machine that a new machine should get back:\n", len(todo))
	show()
	for {
		a, ok := askLine("Enter = save as shown · change: a 2 3 (all machines), m 4 (machines like this), n 5-6 (skip), v 7 (never ask) · l = later (30 days) · q = cancel: ")
		if !ok || strings.EqualFold(a, "q") {
			return nil, false
		}
		if strings.EqualFold(a, "l") {
			if err := client.SnoozeLearn(home, 30*24*time.Hour); err != nil {
				fmt.Fprintln(os.Stderr, err)
			} else {
				fmt.Println("ok: not reminded about these for 30 days (hearth learn shows them any time)")
			}
			return nil, false
		}
		if a == "" {
			return dec, true
		}
		f := strings.Fields(a)
		code := strings.ToLower(f[0])
		if _, known := label[code]; !known || len(f) < 2 {
			fmt.Fprintln(os.Stderr, "start with a, m, n or v, then numbers, e.g. \"n 3 5-6\"")
			continue
		}
		idx, _, err := parsePick(strings.Join(f[1:], " "), len(todo))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		for _, i := range idx {
			dec[todo[i].ID] = code
		}
		show()
	}
}
