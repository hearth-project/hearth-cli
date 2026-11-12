package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/client"
)

// teamRepoCmd binds a git repository to a team (hearth team repo ...).
func teamRepoCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "repo <team>", Short: "The team's git repository: its skills/ and agents/ sync into the team every day; the team's new items go back as a review",
		Long: "A team owner binds a git repository and picks one of their own machines to sync it. That machine, with\n" +
			"the owner's git credentials (the server never gets them), fetches the repository every day or when a\n" +
			"maintainer asks, publishes the skills (skills/**/SKILL.md) and agents (agents/*.md) that changed only there\n" +
			"into the team (the team wins: its changed items are never replaced), and proposes the team's new or changed items back as one review:\n" +
			"refs/for/<branch> (icode / Gerrit, one Change-Id until it is merged), or a branch hearth/team-<team>.\n\n" +
			"  hearth team repo <team>                                show the binding and the last sync\n" +
			"  hearth team repo set <team> <url> --machine <machine>  bind (owners)\n" +
			"  hearth team repo sync <team> [--dry-run]               sync now (on the syncing machine: runs here)\n" +
			"  hearth team repo unset <team>",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			r, err := e.API.TeamRepo(a[0])
			if client.StatusOf(err) == 404 {
				return fmt.Errorf("team %s has no repository: hearth team repo set %s <url> --machine <machine>", a[0], a[0])
			}
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(r)
			}
			printTeamRepo(e, r)
			return nil
		}}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable")

	var ref, machine, push, newDir string
	set := &cobra.Command{Use: "set <team> <url>", Short: "Bind a repository to the team (owners); --machine: one of your machines that syncs it", Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			dev := e.Cfg.DeviceID
			if machine != "" {
				ds, err := e.API.Devices()
				if err != nil {
					return err
				}
				d, err := client.FindDevice(ds, machine)
				if err != nil {
					return err
				}
				dev = d.ID
			}
			r, err := e.API.SetTeamRepo(a[0], api.TeamRepo{URL: a[1], Ref: ref, Device: dev, Push: push, NewDir: newDir})
			if err != nil {
				return err
			}
			fmt.Printf("team %s now syncs %s; %s does it within a few minutes, then every day\n", a[0], r.URL, orDefault(r.DeviceName, r.Device))
			fmt.Printf("check first what it would do: hearth team repo sync %s --dry-run (on %s)\n", a[0], orDefault(r.DeviceName, "that machine"))
			return nil
		}}
	set.Flags().StringVar(&ref, "ref", "", "branch (default: the repository's default branch)")
	set.Flags().StringVar(&machine, "machine", "", "the machine that syncs (one of yours, with git access; default: this one)")
	set.Flags().StringVar(&push, "push", "cr", "how the team's items go back: cr (refs/for/<branch>, a review), branch (hearth/team-<team>), off")
	set.Flags().StringVar(&newDir, "new-dir", "skills", "where new skills from the team go in the repository (skills or a directory under it)")

	var dryRun bool
	sync := &cobra.Command{Use: "sync <team>", Short: "Sync now: on the syncing machine it runs here; elsewhere it asks that machine to", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			r, err := e.API.TeamRepo(a[0])
			if err != nil {
				return err
			}
			if r.Device != e.Cfg.DeviceID {
				if dryRun {
					return fmt.Errorf("--dry-run runs on the syncing machine (%s): hearth ssh %s -- hearth team repo sync %s --dry-run", orDefault(r.DeviceName, r.Device), orDefault(r.DeviceName, r.Device), a[0])
				}
				if err := e.API.RequestTeamRepoSync(a[0]); err != nil {
					return err
				}
				fmt.Printf("asked %s to sync team %s now (within a few minutes; hearth team repo %s shows the result)\n", orDefault(r.DeviceName, r.Device), a[0], a[0])
				return nil
			}
			r.Team = a[0]
			res, err := e.SyncTeamRepo(r, dryRun)
			if dryRun {
				if err != nil {
					return err
				}
				fmt.Printf("would publish to team %s (%d): %s\n", a[0], len(res.Published), listOrNone(res.Published))
				fmt.Printf("would propose to the repository (%d): %s\n", len(res.Proposed), listOrNone(res.Proposed))
				for _, n := range res.Notes {
					fmt.Println("note:", n)
				}
				return nil
			}
			st := api.TeamRepo{Status: "ok", Detail: res.Summary(), Commit: res.Commit, Review: res.Review}
			if err != nil {
				st.Status, st.Detail = "failed", err.Error()
			}
			_ = e.API.ReportTeamRepo(a[0], st)
			if err != nil {
				return err
			}
			fmt.Printf("published to team %s (%d): %s\n", a[0], len(res.Published), listOrNone(res.Published))
			fmt.Printf("proposed to the repository (%d): %s\n", len(res.Proposed), listOrNone(res.Proposed))
			if res.Review != "" {
				fmt.Println("review:", res.Review)
			}
			for _, n := range res.Notes {
				fmt.Println("note:", n)
			}
			return nil
		}}
	sync.Flags().BoolVar(&dryRun, "dry-run", false, "only say what it would publish and propose")

	unset := &cobra.Command{Use: "unset <team>", Short: "Unbind the repository (owners); the team keeps its items", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			return report(e.API.DeleteTeamRepo(a[0]), "team "+a[0]+" no longer syncs a repository")
		}}
	c.AddCommand(set, sync, unset)
	return c
}

func printTeamRepo(e *client.Engine, r api.TeamRepo) {
	branch := orDefault(r.Ref, "(default branch)")
	fmt.Printf("repository  %s  %s\n", r.URL, branch)
	who := orDefault(r.DeviceName, r.Device)
	if r.Device == e.Cfg.DeviceID {
		who += " (this machine)"
	}
	fmt.Printf("synced by   %s, for %s; back as %s\n", who, r.Owner, map[string]string{"cr": "a review (refs/for/" + orDefault(r.Ref, "<branch>") + ")", "branch": "branch hearth/team-" + r.Team, "off": "nothing (push off)"}[orDefault(r.Push, "cr")])
	switch {
	case r.SyncedAt == 0:
		fmt.Println("last sync   not yet")
	default:
		fmt.Printf("last sync   %s, %s: %s\n", ago(time.Unix(r.SyncedAt, 0)), orDefault(r.Status, "?"), r.Detail)
	}
	if r.Review != "" {
		fmt.Println("review      " + r.Review)
	}
	if r.Requested > 0 {
		fmt.Println("asked       a sync is waiting for the machine")
	}
}

// teamRepoDaemon runs the due syncs of the bindings this machine holds.
func teamRepoDaemon(e *client.Engine, logf func(string, ...any)) {
	lines, err := e.TeamRepoJobs(false)
	if err != nil && !errors.Is(err, client.ErrNotLoggedIn) {
		if !strings.Contains(err.Error(), "404") {
			logf("team repositories: %v", err)
		}
		return
	}
	for _, l := range lines {
		logf("%s", l)
	}
}
