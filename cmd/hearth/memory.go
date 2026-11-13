package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
)

// recallCmd searches long-term memory. With --hook it answers an agent's
// prompt hook on stdin; that path reads only the local index.
func recallCmd() *cobra.Command {
	var limit int
	var asJSON, hook, plain bool
	var show, cwd string
	c := &cobra.Command{
		Use:   "recall [words ...]",
		Short: "Search long-term memory: your conversations on every machine and agent, remembered facts, agents' notes (local, milliseconds)",
		RunE: func(cmd *cobra.Command, args []string) error {
			home := client.Home()
			if hook {
				hookFn := client.Hook
				if plain {
					hookFn = client.HookPlain
				}
				out, err := hookFn(home, os.Stdin)
				if err == nil && out != "" {
					fmt.Println(out)
				}
				return nil // a hook must never break the agent's prompt
			}
			if show != "" {
				d, ok := client.MemoryDoc(home, show)
				if !ok {
					return fmt.Errorf("no memory %q (ids come from hearth recall)", show)
				}
				if asJSON {
					return json.NewEncoder(os.Stdout).Encode(d)
				}
				fmt.Printf("%s · %s · %s · %s\n%s\n\n%s\n", d.Kind, d.Agent, d.Machine, time.Unix(d.At, 0).Format("2006-01-02 15:04"), d.Title, d.Body)
				if strings.HasPrefix(d.ID, "s:") {
					fmt.Printf("\nwhole conversation: hearth session restore %s\n", strings.TrimPrefix(d.ID, "s:")[:12])
				}
				return nil
			}
			if len(args) == 0 {
				return fmt.Errorf("what to look for? e.g. hearth recall nccl hang")
			}
			if cwd == "" {
				cwd, _ = os.Getwd()
			}
			start := time.Now()
			hits, err := client.Recall(home, strings.Join(args, " "), cwd, limit)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetEscapeHTML(false)
				return enc.Encode(hits)
			}
			if len(hits) == 0 {
				at, _, _ := client.MemoryIndexAge(home)
				if at.IsZero() {
					fmt.Println("memory is not indexed on this machine yet: hearth memory index")
				} else {
					fmt.Println("nothing found")
				}
				return nil
			}
			fmt.Print(client.FormatHits(hits, 1<<20))
			fmt.Fprintf(os.Stderr, "(%d hit(s) in %s; details: hearth recall --show <id>)\n", len(hits), time.Since(start).Round(time.Millisecond))
			return nil
		},
	}
	c.Flags().IntVarP(&limit, "limit", "n", 5, "results")
	c.Flags().BoolVar(&asJSON, "json", false, "JSON")
	c.Flags().StringVar(&show, "show", "", "print one memory in full (id from the results)")
	c.Flags().StringVar(&cwd, "cwd", "", "rank memories of this directory higher (default: the current one)")
	c.Flags().BoolVar(&hook, "hook", false, "answer a Codex / Claude Code / Comate prompt hook (JSON on stdin)")
	c.Flags().BoolVar(&plain, "plain", false, "with --hook: answer in plain text (Comate)")
	_ = c.Flags().MarkHidden("hook")
	_ = c.Flags().MarkHidden("plain")
	return c
}

func memoryCmd() *cobra.Command {
	c := &cobra.Command{Use: "memory", Short: "Long-term memory shared by your agents and machines: remember, forget, list, index, hooks"}
	var scope string
	var here bool
	var replaces []string
	remember := &cobra.Command{Use: "remember <text>", Short: "Add to key memory, which all your agents get at session start (end-to-end encrypted; 6 KB in all)", Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if here {
				scope, _ = os.Getwd()
			}
			if scope != "" {
				if scope, err = filepath.Abs(scope); err != nil {
					return err
				}
			}
			f, err := e.Remember(strings.Join(a, " "), scope, "user", replaces...)
			if err != nil {
				return err
			}
			where := "everywhere"
			if f.Scope != "" {
				where = "under " + f.Scope
			}
			fmt.Printf("remembered (f:%s, %s); every agent gets it at session start\n", f.ID, where)
			if len(replaces) > 0 {
				fmt.Printf("replaced %s\n", strings.Join(replaces, ", "))
			}
			return nil
		}}
	remember.Flags().StringSliceVar(&replaces, "replace", nil, "ids (f:…) this entry corrects or merges: forgotten once it is stored")
	remember.Flags().StringVar(&scope, "scope", "", "only for this directory tree (e.g. a repository)")
	remember.Flags().BoolVar(&here, "here", false, "only for the current directory tree")
	c.AddCommand(remember,
		&cobra.Command{Use: "forget <id>", Short: "Forget a remembered fact (id f:… from hearth memory list)", Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, a []string) error {
				e, err := engine()
				if err != nil {
					return err
				}
				if err := e.ForgetFact(a[0]); err != nil {
					return err
				}
				fmt.Println("forgotten on all your machines")
				return nil
			}},
		&cobra.Command{Use: "list", Short: "Key memory: what all your agents get at session start", RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			fs, err := e.Facts()
			if err != nil {
				return err
			}
			if len(fs) == 0 {
				fmt.Println(`nothing remembered yet: hearth memory remember "…", or tell your agent "记住……"`)
				return nil
			}
			fmt.Println(keyMemoryUsage(fs))
			for _, f := range fs {
				where := ""
				if f.Scope != "" {
					where = "  [" + f.Scope + "]"
				}
				fmt.Printf("f:%s  %s  %s%s\n", f.ID, time.Unix(f.Created, 0).Format("2006-01-02"), f.Text, where)
			}
			return nil
		}},
		memoryTidyCmd(),
		&cobra.Command{Use: "index", Short: "Bring the local memory index up to date now (the background sync does it every 5 min)", RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			st, err := e.IndexMemory()
			if err != nil {
				return err
			}
			fmt.Printf("indexed %d conversation(s), %d fact(s), %d note(s); %d updated, %d removed in %s\n",
				st.Conversations, st.Facts, st.Notes, st.Updated, st.Removed, st.Took.Round(time.Millisecond))
			return nil
		}},
		&cobra.Command{Use: "status", Short: "What the memory holds here, and the agent hooks", RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			at, n, size := client.MemoryIndexAge(home)
			if !client.MemoryOn(home) {
				fmt.Println("memory is off ([memory] enabled = false)")
			}
			if at.IsZero() {
				fmt.Println("index: not built yet (hearth memory index)")
			} else {
				fmt.Printf("index: %d conversation(s), %d fact(s), %d note(s) · %s · updated %s\n", n["conversation"], n["fact"], n["note"],
					humanize.IBytes(uint64(size)), humanize.Time(at))
			}
			if e, err := engine(); err == nil {
				if fs, err := e.Facts(); err == nil {
					fmt.Println(keyMemoryUsage(fs))
				}
			}
			for _, r := range client.MemoryHookStatus(home) {
				state := "on"
				if r.Op != "installed" {
					state = "not installed (hearth memory hooks on)"
				}
				fmt.Printf("hooks %-6s %s (%s)\n", r.Agent, state, r.Path)
			}
			return nil
		}},
		memoryHooksCmd(),
	)
	return c
}

func memoryHooksCmd() *cobra.Command {
	c := &cobra.Command{Use: "hooks", Short: "Hooks that add key memory and relevant memories to Codex, Claude Code and Comate sessions"}
	set := func(on bool) func(*cobra.Command, []string) error {
		return func(*cobra.Command, []string) error {
			home := client.Home()
			if err := client.SetMemoryConfig(home, nil, &on); err != nil {
				return err
			}
			res, err := client.SyncMemoryHooks(home, on && client.MemoryOn(home))
			printHookResults(res)
			return err
		}
	}
	c.AddCommand(&cobra.Command{Use: "on", Short: "Install the hooks (default)", RunE: set(true)},
		&cobra.Command{Use: "off", Short: "Remove the hooks (recall stays available as a command and MCP tool)", RunE: set(false)})
	return c
}

func printHookResults(res []client.HookResult) {
	for _, r := range res {
		line := fmt.Sprintf("hooks %-6s %-9s %s", r.Agent, r.Op, r.Path)
		if r.Detail != "" {
			line += "  " + r.Detail
		}
		fmt.Println(line)
		if r.Agent == "codex" && r.Op == "installed" {
			fmt.Println("             Codex asks once to trust new hooks: choose \"Trust all and continue\" at its next start")
		}
	}
}

func keyMemoryUsage(fs []client.Fact) string {
	n := client.KeyMemorySize(fs)
	return fmt.Sprintf("key memory: %d entr%s, %s of %s (%d%%)", len(fs), map[bool]string{true: "y", false: "ies"}[len(fs) == 1],
		humanize.IBytes(uint64(n)), humanize.IBytes(client.KeyMemoryLimit), n*100/client.KeyMemoryLimit)
}

// memoryTidyCmd prints key memory for an agent to review with the user:
// Hearth calls no model, so the agent that runs it checks each entry and
// proposes the edits; nothing changes until the user agrees.
func memoryTidyCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "tidy", Short: "Review key memory with your agent: entries to check, rewrite, merge or forget (changes nothing)",
		Long: "Lists key memory with each entry's size and age, flags the long ones, and says how to tidy it. Ask your agent\n" +
			"(\"整理一下 hearth 的记忆\"): it checks every entry against the conversations it came from and the current state,\n" +
			"proposes keep / rewrite / merge / forget, and applies what you agree with hearth memory remember --replace and\n" +
			"hearth memory forget.",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			fs, err := e.Facts()
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(map[string]any{"limit": client.KeyMemoryLimit, "entry_limit": client.FactLimit, "used": client.KeyMemorySize(fs), "entries": fs})
			}
			fmt.Println(keyMemoryUsage(fs))
			if len(fs) == 0 {
				return nil
			}
			fmt.Println()
			for _, f := range fs {
				flag := ""
				if len(f.Text) > client.FactLimit {
					flag = "  TOO LONG"
				}
				who := orDefault(f.Source, "user")
				if f.Machine != "" {
					who += ", " + f.Machine
				}
				fmt.Printf("f:%s  %s  %s  (%s)%s\n", f.ID, time.Unix(f.Created, 0).Format("2006-01-02"), humanize.IBytes(uint64(len(f.Text))), who, flag)
				if f.Scope != "" {
					fmt.Printf("  only under %s\n", f.Scope)
				}
				fmt.Printf("  %s\n\n", f.Text)
			}
			fmt.Print(`To tidy (the agent, with the user):
  1. Check each entry against where it came from (hearth recall "<its words>", hearth recall --show <id>) and the
     current state (the machine, the repository): is it still true, still needed, already said by another entry?
  2. Propose per entry: keep, rewrite shorter, merge with others, or forget (outdated, wrong, or a detail that belongs
     in the conversation). Keep one line each: the conclusion and where it holds; point to details ("details:
     hearth recall --show s:…") instead of copying them.
  3. Apply only what the user agrees to:
       hearth memory remember --replace f:<id>[,f:<id>] "<new text>"   (rewrite or merge)
       hearth memory forget f:<id>
`)
			return nil
		}}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable")
	return c
}
