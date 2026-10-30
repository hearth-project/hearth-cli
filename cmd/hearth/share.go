package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/search"
)

func teamCmd() *cobra.Command {
	c := &cobra.Command{Use: "team", Short: "Teams: share skills and agents with colleagues (create, invite, join, share)",
		Long: "A team shares skills and agents among its members; a team can also let other teams use them.\n\n" +
			"  hearth team create <name>                 you become owner\n" +
			"  hearth team invite <name>                 a code for colleagues: hearth team join <code>\n" +
			"  hearth team share <name> <skill> ...      share from your library\n" +
			"  hearth team share-with <name> <other>     let another team use them too\n\n" +
			"Your teams' skills are found by your agents through skill search; hearth lib link <team>:<skill> keeps one in every agent."}
	withEngine := func(f func(e *client.Engine, args []string) error) func(*cobra.Command, []string) error {
		return func(_ *cobra.Command, args []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			return f(e, args)
		}
	}
	var role string
	add := &cobra.Command{Use: "add <team> <email>", Short: "Add a member or change their role (owner only)", Args: cobra.ExactArgs(2),
		RunE: withEngine(func(e *client.Engine, a []string) error {
			if err := e.API.SetMember(a[0], a[1], role); err != nil {
				return err
			}
			fmt.Printf("%s is now %s of %s\n", a[1], role, a[0])
			return nil
		})}
	add.Flags().StringVar(&role, "role", "member", "member, maintainer (can publish) or owner")
	c.AddCommand(
		&cobra.Command{Use: "create <name>", Short: "Create a team (you become owner); then invite people with hearth team invite", Args: cobra.ExactArgs(1),
			RunE: withEngine(func(e *client.Engine, a []string) error {
				if err := e.API.CreateTeam(a[0]); err != nil {
					return err
				}
				fmt.Printf("created team %s (you are owner)\n", a[0])
				syncTeams(e)
				fmt.Printf("next: hearth team invite %s   (a code others join with)\n      hearth team share %s <skill|agent> ...   (share from your library)\n", a[0], a[0])
				return nil
			})},
		&cobra.Command{Use: "list", Short: "Teams you belong to or can read, with how many skills and agents each shares",
			RunE: withEngine(func(e *client.Engine, _ []string) error {
				teams, _, err := e.SyncTeamSources()
				if err != nil {
					return err
				}
				if len(teams) == 0 {
					fmt.Println("no teams yet: hearth team create <name>, or hearth team join <invite code>")
					return nil
				}
				printTeams(e, teams)
				return nil
			})},
		teamInviteCmd(withEngine),
		&cobra.Command{Use: "join <invite code>", Short: "Join a team with an invite code from one of its members", Args: cobra.ExactArgs(1),
			RunE: withEngine(func(e *client.Engine, a []string) error { return joinTeam(e, a[0]) })},
		&cobra.Command{Use: "leave <team>", Short: "Leave a team", Args: cobra.ExactArgs(1),
			RunE: withEngine(func(e *client.Engine, a []string) error {
				if err := e.API.RemoveMember(a[0], e.Cfg.Email); err != nil {
					return err
				}
				fmt.Printf("left %s\n", a[0])
				syncTeams(e)
				return nil
			})},
		teamShareCmd(withEngine),
		teamUnshareCmd(withEngine),
		teamShareTopCmd(withEngine),
		teamUseTopCmd(withEngine),
		teamShareWithCmd(withEngine),
		&cobra.Command{Use: "members <team>", Short: "List members", Args: cobra.ExactArgs(1),
			RunE: withEngine(func(e *client.Engine, a []string) error {
				ms, err := e.API.Members(a[0])
				for _, m := range ms {
					fmt.Printf("%-32s %s\n", m.Email, m.Role)
				}
				return err
			})},
		add,
		&cobra.Command{Use: "remove <team> <email>", Short: "Remove a member (owners), or leave (yourself)", Args: cobra.ExactArgs(2),
			RunE: withEngine(func(e *client.Engine, a []string) error {
				if err := e.API.RemoveMember(a[0], a[1]); err != nil {
					return err
				}
				fmt.Printf("removed %s from %s\n", a[1], a[0])
				return nil
			})},
		shareUsageCmd(withEngine),
		&cobra.Command{Use: "stats <team>", Short: "Most used skills in a team (last 30 days, from members who share usage)", Args: cobra.ExactArgs(1),
			RunE: withEngine(func(e *client.Engine, a []string) error {
				rs, err := e.API.TeamUsage(a[0], 30)
				if err != nil {
					return err
				}
				if len(rs) == 0 {
					fmt.Println("no usage shared yet (members run `hearth team share-usage`)")
				}
				for i, r := range rs {
					fmt.Printf("%2d. %-5s %-32s %6d uses  %d user(s)\n", i+1, r.Type, r.Name, r.Count, r.Users)
				}
				return nil
			})},
		&cobra.Command{Use: "packages <team>", Short: "Skills and agents published in a team", Args: cobra.ExactArgs(1),
			RunE: withEngine(func(e *client.Engine, a []string) error {
				ps, err := e.API.Packages(a[0])
				tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
				fmt.Fprintln(tw, "TYPE\tNAME\tVERSION\tPUBLISHED BY\tDESCRIPTION")
				for _, p := range ps {
					fmt.Fprintf(tw, "%s\t%s\tv%d\t%s\t%s\n", p.Type, p.Name, p.Version, p.PublishedBy, trunc(p.Description, 60))
				}
				tw.Flush()
				return err
			})},
	)
	return c
}

// syncTeams refreshes the automatic team sources and pushes the library
// when they changed (other machines then see the same teams).
func syncTeams(e *client.Engine) {
	if _, changed, err := e.SyncTeamSources(); err != nil {
		fmt.Printf("team sources: %v\n", err)
	} else if changed {
		if _, err := e.LibPush(false); err != nil {
			fmt.Printf("library not pushed: %v (hearth lib push)\n", err)
		}
	}
}

func joinTeam(e *client.Engine, code string) error {
	t, err := e.API.JoinTeam(code)
	if err != nil {
		return err
	}
	fmt.Printf("joined team %s as %s\n", t.Name, t.Role)
	syncTeams(e)
	if ps, err := e.API.Packages(t.Name); err == nil {
		fmt.Printf("%d shared skill(s)/agent(s) there\n", len(ps))
	}
	applyTeamTop(e, "")
	return nil
}

// applyTeamTop keeps the teams' most used items in every agent now and
// prints what changed (indent prefixes each line).
func applyTeamTop(e *client.Engine, indent string) {
	res, changed, err := e.SyncTeamTop(false)
	if err != nil {
		fmt.Printf("%steam items: %v\n", indent, err)
		return
	}
	var kept []string
	for _, r := range res {
		switch r.Op {
		case "linked", "updated", "unchanged":
			kept = append(kept, r.Type+" "+r.Name)
		case "failed":
			fmt.Printf("%steam %s %s %s: %s\n", indent, r.Source, r.Type, r.Name, r.Detail)
		}
	}
	if changed {
		if _, err := e.Render(false); err != nil {
			fmt.Printf("%srender: %v\n", indent, err)
		}
		if _, err := e.LibPush(false); err != nil {
			fmt.Printf("%slibrary not pushed: %v (hearth lib push)\n", indent, err)
		}
	}
	if len(kept) > 0 {
		fmt.Printf("%sin all your agents from your teams (most used there): %s\n", indent, strings.Join(kept, ", "))
	}
	fmt.Printf("%sthe other team skills are found by skill search (hearth mcp: search_skills)\n", indent)
}

// teamUseTopCmd keeps the teams' most used skills and agents in every agent
// now (the daemon does it every library sync).
func teamUseTopCmd(withEngine func(func(*client.Engine, []string) error) func(*cobra.Command, []string) error) *cobra.Command {
	var dry bool
	c := &cobra.Command{Use: "use-top", Short: "Keep your teams' most used skills and agents in all your agents now (automatic every 20 min; [teams] use_top)",
		RunE: withEngine(func(e *client.Engine, _ []string) error {
			n, _ := client.UseTopN(e.Home)
			res, changed, err := e.SyncTeamTop(dry)
			if err != nil {
				return err
			}
			if changed {
				acts, err := e.Render(false)
				printActions(acts)
				if err != nil {
					return err
				}
				if _, err := e.LibPush(false); err != nil {
					fmt.Printf("library not pushed: %v (hearth lib push)\n", err)
				}
			}
			if len(res) == 0 {
				if n <= 0 {
					fmt.Println("off ([teams] use_top = 0): team items are only found by skill search")
				} else {
					fmt.Println("nothing to keep: no team item used by its members that you do not have yourself")
				}
				return nil
			}
			t := &table{}
			t.add("TEAM", "TYPE", "NAME", "USES", "RESULT", "")
			for _, r := range res {
				uses := ""
				if r.Uses > 0 {
					uses = strconv.FormatInt(r.Uses, 10)
				}
				op := r.Op
				if dry && op != "unchanged" {
					op = "would be " + op
				}
				t.add(r.Source, r.Type, r.Name, uses, op, r.Detail)
			}
			t.fit()
			t.print()
			fmt.Printf("top %d skills and %d agents of each team (members' use, last 30 days), unless you have your own; exclude some: [teams] use_exclude\n", n, n)
			return nil
		})}
	c.Flags().BoolVar(&dry, "dry-run", false, "only show what would change")
	return c
}

func printTeams(e *client.Engine, teams []api.Team) {
	t := &table{}
	t.add("TEAM", "ROLE", "SHARED SKILLS", "SHARED AGENTS")
	for _, tm := range teams {
		role := tm.Role
		if tm.Via != "" {
			role += " (via " + tm.Via + ")"
		}
		sk, ag := "?", "?"
		if ps, err := e.API.Packages(tm.Name); err == nil {
			ns, na := 0, 0
			for _, p := range ps {
				if p.Type == "skill" {
					ns++
				} else {
					na++
				}
			}
			sk, ag = strconv.Itoa(ns), strconv.Itoa(na)
		}
		t.add(tm.Name, role, sk, ag)
	}
	t.print()
}

func teamInviteCmd(withEngine func(func(*client.Engine, []string) error) func(*cobra.Command, []string) error) *cobra.Command {
	var role string
	var days, uses int
	c := &cobra.Command{Use: "invite <team>", Short: "Make an invite code; whoever has it (and an account) joins with hearth team join <code>", Args: cobra.ExactArgs(1),
		RunE: withEngine(func(e *client.Engine, a []string) error {
			inv, err := e.API.CreateTeamInvite(a[0], api.CreateInviteRequest{Role: role, TTLHours: days * 24, MaxUses: uses})
			if err != nil {
				return err
			}
			limit := "any number of people"
			if inv.MaxUses > 0 {
				limit = fmt.Sprintf("%d person(s)", inv.MaxUses)
			}
			fmt.Printf("invite code for %s (role %s, %s, until %s):\n\n  %s\n\n", inv.Team, inv.Role, limit, time.Unix(inv.ExpiresAt, 0).Format("2006-01-02"), inv.Code)
			fmt.Printf("they run:  hearth team join %s\n  or on a new machine:  hearth setup --team %s\n", inv.Code, inv.Code)
			return nil
		})}
	c.Flags().StringVar(&role, "role", "maintainer", "role of who joins: member (use shared items), maintainer (also share), owner")
	c.Flags().IntVar(&days, "days", 7, "valid for this many days")
	c.Flags().IntVar(&uses, "uses", 0, "how many people can use it (0 = unlimited until it expires)")
	return c
}

// teamShareCmd publishes library skills/agents to a team (type looked up).
func teamShareCmd(withEngine func(func(*client.Engine, []string) error) func(*cobra.Command, []string) error) *cobra.Command {
	var allowSecrets bool
	c := &cobra.Command{Use: "share <team> <skill|agent> ...", Short: "Share skills or agents of your library with a team (a new version each time they change)", Args: cobra.MinimumNArgs(2),
		RunE: withEngine(func(e *client.Engine, a []string) error {
			lib, err := (library.Library{Root: client.LibraryDir(e.Home)}).Load()
			if err != nil {
				return err
			}
			kind := map[string]string{}
			for _, ag := range lib.Agents {
				kind[ag.Name] = "agent"
			}
			for _, sk := range lib.Skills {
				kind[sk.Name] = "skill" // a skill wins a name clash; use hearth lib publish agent <name>
			}
			failed := 0
			for _, name := range a[1:] {
				typ := kind[name]
				if typ == "" {
					fmt.Printf("  %-24s not in your library (hearth lib status)\n", name)
					failed++
					continue
				}
				v, created, err := e.Publish(a[0], typ, name, allowSecrets)
				switch {
				case err != nil:
					fmt.Printf("  %-24s %v\n", name, err)
					failed++
				case created:
					fmt.Printf("  %-24s shared as %s v%d\n", name, typ, v)
				default:
					fmt.Printf("  %-24s unchanged (v%d)\n", name, v)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d item(s) not shared", failed)
			}
			fmt.Printf("members of %s find them through skill search; hearth lib link %s:<name> keeps one in every agent\n", a[0], a[0])
			return nil
		})}
	c.Flags().BoolVar(&allowSecrets, "allow-secrets", false, "share even if the secret scan finds something")
	return c
}

// teamUnshareCmd removes shared skills or agents from a team, every version.
func teamUnshareCmd(withEngine func(func(*client.Engine, []string) error) func(*cobra.Command, []string) error) *cobra.Command {
	var typ string
	c := &cobra.Command{Use: "unshare <team> <skill|agent> ...", Short: "Remove skills or agents from a team, with every version shared (maintainers)", Args: cobra.MinimumNArgs(2),
		RunE: withEngine(func(e *client.Engine, a []string) error {
			failed := 0
			for _, name := range a[1:] {
				types := []string{"skill", "agent"}
				if typ != "" {
					types = []string{typ}
				}
				done := false
				for _, t := range types {
					n, err := e.API.Unpublish(a[0], t, name)
					if client.StatusOf(err) == 404 {
						continue
					}
					if err != nil {
						fmt.Printf("  %-24s %v\n", name, err)
						failed++
					} else {
						fmt.Printf("  %-24s removed (%s, %d version(s))\n", name, t, n)
					}
					done = true
					break
				}
				if !done {
					fmt.Printf("  %-24s not shared in %s\n", name, a[0])
					failed++
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d item(s) not removed", failed)
			}
			return nil
		})}
	c.Flags().StringVar(&typ, "type", "", "skill or agent (default: whichever is shared under that name)")
	return c
}

// teamShareWithCmd lets another team read this team's skills and agents.
func teamShareWithCmd(withEngine func(func(*client.Engine, []string) error) func(*cobra.Command, []string) error) *cobra.Command {
	var off bool
	c := &cobra.Command{Use: "share-with <team> [other-team]", Short: "Let another team's members use this team's skills and agents (owners; --off to stop); no other team: list", Args: cobra.RangeArgs(1, 2),
		RunE: withEngine(func(e *client.Engine, a []string) error {
			if len(a) == 2 {
				if err := e.API.ShareTeam(a[0], a[1], !off); err != nil {
					return err
				}
				if off {
					fmt.Printf("%s no longer sees %s's skills and agents\n", a[1], a[0])
				} else {
					fmt.Printf("members of %s can now use %s's skills and agents (read-only)\n", a[1], a[0])
				}
			}
			sh, err := e.API.TeamShares(a[0])
			if err != nil {
				return err
			}
			fmt.Printf("%s shares with: %s\n", a[0], listOrNone(sh.SharedWith))
			fmt.Printf("shared with %s by: %s\n", a[0], listOrNone(sh.SharedFrom))
			return nil
		})}
	c.Flags().BoolVar(&off, "off", false, "stop sharing with that team")
	return c
}

func listOrNone(l []string) string {
	if len(l) == 0 {
		return "-"
	}
	return strings.Join(l, ", ")
}

func sourceCmd() *cobra.Command {
	c := &cobra.Command{Use: "source", Short: "Where shared skills/agents come from (searched after the local library, in order)"}
	var ref, subdir string
	var trusted bool
	addTeam := &cobra.Command{Use: "team <team> [name]", Short: "Use a team's packages", Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, a []string) error {
			name := a[0]
			if len(a) == 2 {
				name = a[1]
			}
			return report(client.AddSource(client.Home(), client.Source{Name: name, Type: "team", Team: a[0]}), "added source "+name)
		}}
	addGit := &cobra.Command{Use: "git <name> <url>", Short: "Use a git repository of skills (skills/<name>/SKILL.md or a flat list)", Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			trust := "untrusted"
			if trusted {
				trust = "trusted"
			}
			return report(client.AddSource(client.Home(), client.Source{Name: a[0], Type: "git", URL: a[1], Ref: ref, Subdir: subdir, Trust: trust}), "added source "+a[0]+" ("+trust+")")
		}}
	addGit.Flags().StringVar(&ref, "ref", "", "branch or tag (default: the remote's default branch)")
	addGit.Flags().StringVar(&subdir, "subdir", "", "directory in the repo that contains skills/ (or the skill directories)")
	addGit.Flags().BoolVar(&trusted, "trusted", false, "skip the review step when linking from this source")
	add := &cobra.Command{Use: "add", Short: "Add a source"}
	add.AddCommand(addTeam, addGit)
	c.AddCommand(add,
		&cobra.Command{Use: "list", Short: "List sources in lookup order", RunE: func(*cobra.Command, []string) error {
			ss, err := client.LoadSources(client.Home())
			fmt.Println("1. local library (always first)")
			for i, s := range ss {
				where := s.Team
				if s.Type == "git" {
					where = strings.TrimSpace(s.URL + " " + s.Ref + " " + s.Subdir)
				}
				fmt.Printf("%d. %-16s %-4s %-10s %s\n", i+2, s.Name, s.Type, s.Trust, where)
			}
			return err
		}},
		&cobra.Command{Use: "rm <name>", Short: "Remove a source", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			return report(client.RemoveSource(client.Home(), a[0]), "removed source "+a[0])
		}},
	)
	return c
}

func report(err error, ok string) error {
	if err == nil {
		fmt.Println(ok)
	}
	return err
}

func typeOf(agent bool) string {
	if agent {
		return "agent"
	}
	return "skill"
}

func findCmd() *cobra.Command {
	var limit int
	var refresh bool
	c := &cobra.Command{Use: "find <query>", Short: "Search every source for shared skills and agents", Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			e.Refresh = refresh
			q := strings.Join(args, " ")
			ss, err := client.LoadSources(e.Home)
			if err != nil {
				return err
			}
			type hit struct {
				kind, ref, desc string
				score           float64
			}
			var hits []hit
			// Teams: one server-side search each (in parallel inside SearchTeams
			// for trusted ones); untrusted teams are searched too, for linking.
			for _, s := range ss {
				if s.Type != "team" {
					continue
				}
				res, err := e.API.SearchTeam(s.Team, q, limit)
				if err != nil {
					fmt.Println("warning:", s.Name, err)
					continue
				}
				for _, h := range res {
					hits = append(hits, hit{h.Type, s.Name + ":" + h.Name, h.Description, h.Score})
				}
			}
			// Git sources: indexed locally from a cached checkout.
			var docs []search.Skill
			kind := map[string]string{}
			for _, s := range ss {
				if s.Type != "git" {
					continue
				}
				cands, err := e.Index(s)
				if err != nil {
					fmt.Println("warning:", err)
					continue
				}
				for _, c := range cands {
					kind[c.Ref()] = c.Type
					docs = append(docs, search.NewSkill(c.Ref(), c.Description, "", s.Name))
				}
			}
			for _, r := range search.Build(docs).Search(q, limit) {
				hits = append(hits, hit{kind[r.Name], r.Name, r.Description, r.Score})
			}
			sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
			if len(hits) > limit {
				hits = hits[:limit]
			}
			for i, h := range hits {
				fmt.Printf("%d. %-5s %-36s %s\n", i+1, h.kind, h.ref, trunc(h.desc, 70))
			}
			return nil
		}}
	c.Flags().IntVar(&limit, "limit", 10, "results")
	c.Flags().BoolVar(&refresh, "refresh", false, "fetch git sources even if fetched recently")
	return c
}

func linkCmd() *cobra.Command {
	var agent, yes bool
	c := &cobra.Command{Use: "link <name|source:name>", Short: "Use a shared skill (or --agent) in every agent, pinned to its current version", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			cand, err := e.ResolvePackage(a[0], typeOf(agent))
			if err != nil {
				return err
			}
			dir, sum, err := e.Fetch(cand)
			if err != nil {
				return err
			}
			if cand.Source.Trust != "trusted" && !yes {
				review(dir)
				fmt.Printf("\n%s comes from untrusted source %q. Review the content above, then re-run with --yes to link it.\n", cand.Ref(), cand.Source.Name)
				return nil
			}
			l, err := e.AddLink(cand, sum)
			if err != nil {
				return err
			}
			fmt.Printf("linked %s %s from %s (%s)\n", l.Type, l.Name, l.Source, pin(l))
			return renderAfterChange(e)
		}}
	c.Flags().BoolVar(&agent, "agent", false, "link an agent instead of a skill")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "confirm linking from an untrusted source")
	return c
}

func pin(l client.Link) string {
	if l.Commit != "" {
		return "commit " + l.Commit[:min(12, len(l.Commit))]
	}
	return fmt.Sprintf("v%d", l.Version)
}

func renderAfterChange(e *client.Engine) error {
	acts, err := e.Render(false)
	printActions(acts)
	if err == nil {
		fmt.Println("the link is stored in your library; `hearth lib push` (or the daemon) shares it with your other machines")
	}
	return err
}

// review prints what an untrusted package would put in front of the agent.
func review(dir string) {
	fmt.Printf("--- files in %s\n", dir)
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		mark := ""
		if info, err := d.Info(); err == nil && info.Mode()&0o111 != 0 {
			mark = "  [executable]"
		}
		fmt.Printf("  %s%s\n", rel, mark)
		return nil
	})
	for _, name := range []string{"SKILL.md", "AGENT.md"} {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		fmt.Printf("--- %s (first 60 lines)\n", name)
		sc := bufio.NewScanner(f)
		for i := 0; i < 60 && sc.Scan(); i++ {
			fmt.Println("  " + sc.Text())
		}
		f.Close()
	}
}

func unlinkCmd() *cobra.Command {
	var agent bool
	c := &cobra.Command{Use: "unlink <name>", Short: "Stop using a shared skill (or --agent)", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if err := e.Unlink(typeOf(agent), a[0]); err != nil {
				return err
			}
			fmt.Println("unlinked", a[0])
			return renderAfterChange(e)
		}}
	c.Flags().BoolVar(&agent, "agent", false, "unlink an agent")
	return c
}

func linksCmd() *cobra.Command {
	return &cobra.Command{Use: "links", Short: "List linked shared skills and agents", RunE: func(*cobra.Command, []string) error {
		ls, err := client.LoadLinks(client.Home())
		for _, l := range ls {
			fmt.Printf("%-5s %-28s %-16s %s\n", l.Type, l.Name, l.Source, pin(l))
		}
		return err
	}}
}

func updateCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{Use: "update [name]", Short: "Show (and with --yes apply) newer versions of linked items", Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			name := ""
			if len(a) == 1 {
				name = a[0]
			}
			ups, err := e.CheckUpdates(name)
			if err != nil {
				return err
			}
			if len(ups) == 0 {
				fmt.Println("everything is up to date")
				return nil
			}
			for _, u := range ups {
				fmt.Printf("%s %s (%s): %s -> %s\n", u.Link.Type, u.Link.Name, u.Link.Source, pin(u.Link),
					pin(client.Link{Version: u.New.Version, Commit: u.New.Commit}))
				for _, ch := range u.Changes {
					fmt.Println("    " + ch)
				}
				if yes {
					if u.New.Source.Trust != "trusted" {
						fmt.Printf("    review: %s\n", u.NewDir)
					}
					if _, err := e.AddLink(u.New, u.NewSum); err != nil {
						return err
					}
				}
			}
			if !yes {
				fmt.Println("\nre-run with --yes to apply")
				return nil
			}
			return renderAfterChange(e)
		}}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "apply the updates")
	return c
}

func publishCmd() *cobra.Command {
	var team string
	var allowSecrets bool
	c := &cobra.Command{Use: "publish skill|agent <name> --team <team>", Short: "Publish a library skill or agent as a new immutable version (maintainers)", Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			v, created, err := e.Publish(team, a[0], a[1], allowSecrets)
			if err != nil {
				return err
			}
			if !created {
				fmt.Printf("%s %s is unchanged; latest is still v%d\n", a[0], a[1], v)
				return nil
			}
			fmt.Printf("published %s %s v%d to %s at %s\n", a[0], a[1], v, team, time.Now().Format(time.DateTime))
			return nil
		}}
	c.Flags().StringVar(&team, "team", "", "team")
	c.Flags().BoolVar(&allowSecrets, "allow-secrets", false, "publish even if the secret scan finds something")
	c.MarkFlagRequired("team")
	return c
}

func shareUsageCmd(withEngine func(func(*client.Engine, []string) error) func(*cobra.Command, []string) error) *cobra.Command {
	var auto, off bool
	c := &cobra.Command{Use: "share-usage <team>", Short: "Upload your usage counts of this team's published skills (opt-in, last 30 days)", Args: cobra.ExactArgs(1),
		RunE: withEngine(func(e *client.Engine, a []string) error {
			if off {
				return report(client.SetAutoShare(e.Home, a[0], false), "stopped sharing usage with "+a[0])
			}
			n, err := e.ShareUsage(a[0])
			if err != nil {
				return err
			}
			fmt.Printf("shared %d daily count(s) with %s (only skills published there)\n", n, a[0])
			if auto {
				return report(client.SetAutoShare(e.Home, a[0], true), "the daemon will keep sharing hourly")
			}
			return nil
		})}
	c.Flags().BoolVar(&auto, "auto", false, "keep sharing from the daemon (hourly, synced setting)")
	c.Flags().BoolVar(&off, "off", false, "stop automatic sharing")
	return c
}

// teamShareTopCmd runs the automatic sharing of the most used skills and
// agents now (the daemon does it every library sync).
func teamShareTopCmd(withEngine func(func(*client.Engine, []string) error) func(*cobra.Command, []string) error) *cobra.Command {
	var dry bool
	c := &cobra.Command{Use: "share-top", Short: "Share your most used skills and agents with your teams now (automatic every 20 min; [teams] share_top in config.toml)",
		RunE: withEngine(func(e *client.Engine, _ []string) error {
			n, _ := client.ShareTopN(e.Home)
			if n <= 0 {
				fmt.Println("automatic sharing is off ([teams] share_top = 0 in hearth config --edit)")
				return nil
			}
			res, err := e.ShareTop(dry)
			if err != nil {
				return err
			}
			if len(res) == 0 {
				fmt.Println("nothing to share: no team where you can publish (maintainer or owner), or no usage of your own skills/agents yet")
				return nil
			}
			t := &table{}
			t.add("TEAM", "TYPE", "NAME", "RESULT", "")
			for _, r := range res {
				v := ""
				if r.Version > 0 {
					v = fmt.Sprintf("v%d", r.Version)
				}
				t.add(r.Team, r.Type, r.Name, r.Op, strings.TrimSpace(v+" "+r.Detail))
			}
			t.fit()
			t.print()
			fmt.Printf("top %d skills and %d agents of your own library (all your machines, last 30 days); exclude some: [teams] share_exclude\n", n, n)
			return nil
		})}
	c.Flags().BoolVar(&dry, "dry-run", false, "only show what would be shared")
	return c
}
