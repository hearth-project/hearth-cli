package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/server"
)

// parseQuota accepts "10GiB", "500MB", "0"/"unlimited", or "default" (-1).
func parseQuota(v string) (int64, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "unlimited", "none":
		return 0, nil
	case "default":
		return -1, nil
	}
	n, err := humanize.ParseBytes(v)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q (e.g. 10GiB, 500MB, unlimited)", v)
	}
	return int64(n), nil
}

func quotaString(q int64) string {
	if q <= 0 {
		return "unlimited"
	}
	return humanize.IBytes(uint64(q))
}

func bytesString(n int64) string { return humanize.IBytes(uint64(max(n, 0))) }

func ago(t time.Time) string {
	if t.Unix() <= 0 {
		return "never"
	}
	return humanize.Time(t)
}

// adminCmd groups administration commands. Like `server invite`, they open
// the server's storage directly, so only someone with access to it (the
// data directory or the database credentials) can run them.
func adminCmd() *cobra.Command {
	var sto storage
	var quota string
	var asJSON bool
	open := func() (*server.Server, error) {
		cfg := sto.config()
		cfg.Logger = log.New(io.Discard, "", 0)
		if quota != "" {
			q, err := parseQuota(quota)
			if err != nil {
				return nil, err
			}
			cfg.DefaultQuota = max(q, 0)
		} else if v := os.Getenv("HEARTH_QUOTA"); v != "" {
			q, err := parseQuota(v)
			if err != nil {
				return nil, err
			}
			cfg.DefaultQuota = max(q, 0)
		}
		return server.New(cfg)
	}
	with := func(f func(*cobra.Command, *server.Server, []string) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, a []string) error {
			srv, err := open()
			if err != nil {
				return err
			}
			defer srv.Close()
			return f(cmd, srv, a)
		}
	}
	printJSON := func(v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err == nil {
			fmt.Println(string(b))
		}
		return err
	}
	c := &cobra.Command{
		Use:   "admin",
		Short: "Administer accounts, quotas, invites and storage (run with access to the server's storage)",
		Long: "Opens the server's database and blob store directly (same --data/--db/--blobs as `hearth server`),\n" +
			"so it works while the server runs and needs no admin password. Changes reach running servers within 30 s.\n" +
			"Pass the same --quota as the server (or set $HEARTH_QUOTA) so default quotas are shown correctly.",
	}
	sto.persistentFlags(c)
	c.PersistentFlags().StringVar(&quota, "quota", "", "server default quota, as given to hearth server --quota [$HEARTH_QUOTA]")
	c.PersistentFlags().BoolVar(&asJSON, "json", false, "print JSON")

	c.AddCommand(
		&cobra.Command{Use: "stats", Short: "Totals for the whole server", RunE: with(func(_ *cobra.Command, srv *server.Server, _ []string) error {
			st, err := srv.Stats()
			if err != nil || asJSON {
				return jsonOrErr(err, printJSON, st)
			}
			fmt.Printf("storage:   %s\n", st.Storage)
			fmt.Printf("users:     %d (%d disabled, %d active in the last 30 days)\n", st.Users, st.DisabledUsers, st.ActiveUsers30d)
			fmt.Printf("devices:   %d active\n", st.Devices)
			fmt.Printf("sessions:  %d (%d expired), %s\n", st.Sessions, st.ExpiredSessions, bytesString(st.SessionBytes))
			fmt.Printf("libraries: %s\n", bytesString(st.LibraryBytes))
			fmt.Printf("teams:     %d, %d package versions (%s)\n", st.Teams, st.Packages, bytesString(st.PackageBytes))
			if st.PendingDeletes > 0 {
				fmt.Printf("pending blob deletes: %d (hearth server admin gc)\n", st.PendingDeletes)
			}
			return nil
		})},
		&cobra.Command{Use: "users", Short: "List accounts with their usage (largest first)", RunE: with(func(_ *cobra.Command, srv *server.Server, _ []string) error {
			us, err := srv.Users()
			if err != nil || asJSON {
				return jsonOrErr(err, printJSON, us)
			}
			fmt.Printf("%-32s %-8s %10s %10s %8s %4s  %-14s %s\n", "EMAIL", "STATUS", "USED", "QUOTA", "SESSIONS", "DEV", "LAST SEEN", "CREATED")
			for _, u := range us {
				fmt.Printf("%-32s %-8s %10s %10s %8d %4d  %-14s %s\n", u.Email, u.Status, bytesString(u.Used()), quotaString(u.QuotaBytes),
					u.Sessions, u.Devices, ago(u.LastSeen), u.CreatedAt.Format("2006-01-02"))
			}
			return nil
		})},
		&cobra.Command{Use: "user <email>", Short: "Show one account", Args: cobra.ExactArgs(1), RunE: with(func(_ *cobra.Command, srv *server.Server, a []string) error {
			u, err := srv.User(a[0])
			if err != nil || asJSON {
				return jsonOrErr(err, printJSON, u)
			}
			pct := ""
			if u.QuotaBytes > 0 {
				pct = fmt.Sprintf(" (%.0f%%)", 100*float64(u.Used())/float64(u.QuotaBytes))
			}
			fmt.Printf("email:     %s\nstatus:    %s\ncreated:   %s\nlast seen: %s\n", u.Email, u.Status, u.CreatedAt.Format(time.DateTime), ago(u.LastSeen))
			fmt.Printf("used:      %s of %s%s (quota from %s)\n", bytesString(u.Used()), quotaString(u.QuotaBytes), pct, u.QuotaSource)
			fmt.Printf("  sessions %d (%d expired), %s; library %s; %d secrets, %s\n", u.Sessions, u.Expired, bytesString(u.SessionBytes),
				bytesString(u.LibraryBytes), u.Secrets, bytesString(u.SecretBytes))
			fmt.Printf("devices:   %d active\nteams:     %s\n", u.Devices, strings.Join(u.Teams, ", "))
			return nil
		})},
		&cobra.Command{Use: "disable <email>", Short: "Block login and every token of an account (data is kept)", Args: cobra.ExactArgs(1),
			RunE: with(func(_ *cobra.Command, srv *server.Server, a []string) error {
				err := srv.SetUserStatus(a[0], false)
				if err == nil {
					srv.Audit("cli", "disable-user", a[0], "")
				}
				return report(err, "disabled "+a[0]+" (running servers apply it within 30 s)")
			})},
		&cobra.Command{Use: "enable <email>", Aliases: []string{"approve"}, Short: "Re-enable a disabled account, or approve one waiting (registration approval)", Args: cobra.ExactArgs(1),
			RunE: with(func(_ *cobra.Command, srv *server.Server, a []string) error {
				err := srv.SetUserStatus(a[0], true)
				if err == nil {
					srv.Audit("cli", "enable-user", a[0], "")
				}
				return report(err, "enabled "+a[0])
			})},
		&cobra.Command{Use: "quota <email> <size|unlimited|default>", Short: "Set an account's storage quota (e.g. 20GiB)", Args: cobra.ExactArgs(2),
			RunE: with(func(_ *cobra.Command, srv *server.Server, a []string) error {
				q, err := parseQuota(a[1])
				if err != nil {
					return err
				}
				err = srv.SetUserQuota(a[0], q)
				if err == nil {
					srv.Audit("cli", "set-quota", a[0], a[1])
				}
				return report(err, "quota of "+a[0]+": "+a[1])
			})},
		&cobra.Command{Use: "revoke-devices <email>", Short: "Log out every device of an account (e.g. after a leaked password)", Args: cobra.ExactArgs(1),
			RunE: with(func(_ *cobra.Command, srv *server.Server, a []string) error {
				n, err := srv.RevokeUserDevices(a[0])
				return report(err, fmt.Sprintf("revoked %d device(s) of %s; the user logs in again with the master password", n, a[0]))
			})},
		deleteUserCmd(with),
		&cobra.Command{Use: "grant <email>", Short: "Make an account an admin of this server (it can use the admin panel: hearth panel)", Args: cobra.ExactArgs(1),
			RunE: with(func(_ *cobra.Command, srv *server.Server, a []string) error {
				err := srv.SetAdmin(a[0], true)
				if err == nil {
					srv.Audit("cli", "add-admin", a[0], "")
				}
				return report(err, a[0]+" is an admin; on a machine logged in as "+a[0]+": hearth panel")
			})},
		&cobra.Command{Use: "ungrant <email>", Short: "Remove an admin (not the last one)", Args: cobra.ExactArgs(1),
			RunE: with(func(_ *cobra.Command, srv *server.Server, a []string) error {
				err := srv.SetAdmin(a[0], false)
				if err == nil {
					srv.Audit("cli", "remove-admin", a[0], "")
				}
				return report(err, a[0]+" is no longer an admin")
			})},
		&cobra.Command{Use: "admins", Short: "List admins", RunE: with(func(_ *cobra.Command, srv *server.Server, _ []string) error {
			as, err := srv.Admins()
			if err != nil || asJSON {
				return jsonOrErr(err, printJSON, as)
			}
			if len(as) == 0 {
				fmt.Println("no admins yet: hearth server admin grant <email>")
			}
			for _, a := range as {
				fmt.Println(a)
			}
			return nil
		})},
		panelLinkCmd(with),
		invitesCmd(with, printJSON, &asJSON),
		gcCmd(with),
	)
	return c
}

// jsonOrErr returns err, or prints v as JSON when there is no error.
func jsonOrErr(err error, printJSON func(any) error, v any) error {
	if err != nil {
		return err
	}
	return printJSON(v)
}

type withFn = func(func(*cobra.Command, *server.Server, []string) error) func(*cobra.Command, []string) error

func deleteUserCmd(with withFn) *cobra.Command {
	var yes bool
	c := &cobra.Command{Use: "delete-user <email>", Short: "Delete an account and all its sessions, library and secrets", Args: cobra.ExactArgs(1),
		RunE: with(func(_ *cobra.Command, srv *server.Server, a []string) error {
			if !yes {
				u, err := srv.User(a[0])
				if err != nil {
					return err
				}
				return fmt.Errorf("this permanently deletes %s: %d sessions (%s), library and %d secrets; re-run with --yes",
					a[0], u.Sessions, bytesString(u.SessionBytes), u.Secrets)
			}
			err := srv.DeleteUser(a[0])
			if err == nil {
				srv.Audit("cli", "delete-user", a[0], "")
			}
			return report(err, "deleted "+a[0])
		})}
	c.Flags().BoolVar(&yes, "yes", false, "confirm")
	return c
}

func invitesCmd(with withFn, printJSON func(any) error, asJSON *bool) *cobra.Command {
	c := &cobra.Command{Use: "invites", Short: "List invites (create with `hearth server invite`)",
		RunE: with(func(_ *cobra.Command, srv *server.Server, _ []string) error {
			in, err := srv.Invites()
			if err != nil || *asJSON {
				return jsonOrErr(err, printJSON, in)
			}
			now := time.Now()
			for _, i := range in {
				state := "unused, expires " + humanize.Time(i.ExpiresAt)
				switch {
				case i.UsedBy != "":
					state = "used by " + i.UsedBy
				case i.ExpiresAt.Before(now):
					state = "expired"
				}
				fmt.Printf("%s  created %s  %s\n", i.ID, i.CreatedAt.Format("2006-01-02 15:04"), state)
			}
			return nil
		})}
	c.AddCommand(&cobra.Command{Use: "revoke <id>", Short: "Revoke an unused invite", Args: cobra.ExactArgs(1),
		RunE: with(func(_ *cobra.Command, srv *server.Server, a []string) error {
			return report(srv.RevokeInvite(a[0]), "revoked invite "+a[0])
		})})
	return c
}

func gcCmd(with withFn) *cobra.Command {
	var dry bool
	var minAge time.Duration
	c := &cobra.Command{Use: "gc", Short: "Run session expiry now and delete orphaned blobs",
		Long: "Runs one expiry pass (as the server does hourly, with the server's default expiry settings), finishes queued\n" +
			"blob deletes, then removes blobs no database row refers to (older than --min-age).",
		RunE: with(func(_ *cobra.Command, srv *server.Server, _ []string) error {
			if !dry {
				st, err := srv.RunGC()
				if err != nil {
					return err
				}
				fmt.Printf("expiry: %d expired, %d deleted, %d tombstones purged\n", st.Expired, st.Deleted, st.TombstonesPurged)
			}
			sw, err := srv.SweepOrphans(minAge, dry)
			if err != nil {
				return err
			}
			verb := "deleted"
			if dry {
				verb = "would delete"
			}
			fmt.Printf("blobs: %d stored, %d referenced; %s %d orphans (%s)\n", sw.Blobs, sw.Referenced, verb, sw.Orphans, bytesString(sw.Bytes))
			return nil
		})}
	c.Flags().BoolVar(&dry, "dry-run", false, "only report")
	c.Flags().DurationVar(&minAge, "min-age", time.Hour, "keep orphans younger than this (uploads in flight)")
	return c
}

// panelLinkCmd prints a sign-in link to the admin panel (server side).
func panelLinkCmd(with withFn) *cobra.Command {
	var url string
	c := &cobra.Command{Use: "link <email>", Short: "Print a sign-in link to the admin panel for an admin (valid 10 minutes)", Args: cobra.ExactArgs(1),
		RunE: with(func(_ *cobra.Command, srv *server.Server, a []string) error {
			if url == "" {
				url = os.Getenv("HEARTH_PUBLIC_URL")
			}
			if url == "" {
				return fmt.Errorf("pass --url <the server's public URL> (as given to hearth server --public-url)")
			}
			l, err := srv.AdminLink(url, a[0])
			if err != nil {
				return err
			}
			fmt.Println(l)
			return nil
		})}
	c.Flags().StringVar(&url, "url", "", "the server's public URL [$HEARTH_PUBLIC_URL]")
	return c
}
