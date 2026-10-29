package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/hearth-project/hearth-cli/internal/client"
)

func secretCmd() *cobra.Command {
	c := &cobra.Command{Use: "secret", Short: "End-to-end encrypted credentials (tokens, config files, keys)"}
	var file, restoreTo string
	set := &cobra.Command{
		Use:   "set <NAME>",
		Short: "Store a secret (value from --file, stdin, or a hidden prompt)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			var val []byte
			switch {
			case file != "":
				if val, err = os.ReadFile(file); err != nil {
					return err
				}
				if restoreTo == "" {
					abs, _ := filepath.Abs(file)
					restoreTo = tildePath(abs)
				}
			case term.IsTerminal(int(os.Stdin.Fd())):
				fmt.Fprint(os.Stderr, "Value: ")
				val, err = term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
			default:
				val, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
				val = bytes.TrimSuffix(val, []byte("\n"))
			}
			if err != nil {
				return err
			}
			if bytes.Contains(val, []byte("PRIVATE KEY-----")) {
				fmt.Fprintln(os.Stderr, "warning: this is a private key. Prefer SSH agent forwarding (ssh -A) where possible; if this key leaks, rotate it everywhere.")
			}
			if err := e.SetSecret(client.Secret{Name: a[0], Value: val, RestoreTo: restoreTo}); err != nil {
				return err
			}
			fmt.Printf("stored %s (%d bytes)", a[0], len(val))
			if restoreTo != "" {
				fmt.Printf(", restored to %s on other machines", restoreTo)
			} else if client.IsEnvName(a[0]) {
				fmt.Printf(", available to `hearth secret exec` as $%s", a[0])
			}
			fmt.Println()
			return nil
		},
	}
	set.Flags().StringVar(&file, "file", "", "read the value from a file (e.g. ~/.config/gh/hosts.yml); restored to the same path by default")
	set.Flags().StringVar(&restoreTo, "restore-to", "", "path to write the secret to on restore (~/... or absolute)")

	var force bool
	restore := &cobra.Command{
		Use:   "restore",
		Short: "Write file secrets back to their paths (mode 0600)",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			return restoreSecrets(e, force)
		},
	}
	restore.Flags().BoolVar(&force, "force", false, "overwrite existing files that differ")

	c.AddCommand(set, restore,
		&cobra.Command{Use: "get <NAME>", Short: "Print a secret's value", Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, a []string) error {
				e, err := engine()
				if err != nil {
					return err
				}
				s, err := e.Secret(a[0])
				if err != nil {
					return err
				}
				os.Stdout.Write(s.Value)
				if term.IsTerminal(int(os.Stdout.Fd())) && !bytes.HasSuffix(s.Value, []byte("\n")) {
					fmt.Println()
				}
				return nil
			}},
		&cobra.Command{Use: "list", Short: "List secret names (not values)",
			RunE: func(*cobra.Command, []string) error {
				e, err := engine()
				if err != nil {
					return err
				}
				all, err := e.Secrets()
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
				fmt.Fprintln(tw, "NAME\tSIZE\tUSE\tUPDATED")
				facts := 0
				for _, s := range all {
					if strings.HasPrefix(s.Name, "memory.") {
						facts++ // remembered facts: hearth memory list
						continue
					}
					use := "-"
					if strings.HasPrefix(s.Name, "hearth.") {
						use = "hearth internal (recipe approvals)"
					} else if s.RestoreTo != "" {
						use = "file " + s.RestoreTo
					} else if client.IsEnvName(s.Name) {
						use = "env $" + s.Name
					}
					fmt.Fprintf(tw, "%s\t%d B\t%s\t%s\n", s.Name, len(s.Value), use, humanize.Time(time.Unix(s.UpdatedAt, 0)))
				}
				if err := tw.Flush(); err != nil {
					return err
				}
				if facts > 0 {
					fmt.Printf("(+ %d remembered fact(s), stored encrypted the same way: hearth memory list)\n", facts)
				}
				return nil
			}},
		&cobra.Command{Use: "rm <NAME>", Short: "Delete a secret", Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, a []string) error {
				e, err := engine()
				if err != nil {
					return err
				}
				return report(e.DeleteSecret(a[0]), "deleted "+a[0]+" (rotate it if it may have leaked)")
			}},
		&cobra.Command{Use: "exec -- <command> [args...]", Short: "Run a command with env-style secrets (UPPER_CASE names) in its environment",
			Args: cobra.MinimumNArgs(1), DisableFlagParsing: false,
			RunE: func(_ *cobra.Command, a []string) error {
				e, err := engine()
				if err != nil {
					return err
				}
				env, err := e.SecretEnv()
				if err != nil {
					return err
				}
				cmd := exec.Command(a[0], a[1:]...)
				cmd.Env = append(os.Environ(), env...)
				cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
				err = cmd.Run()
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					os.Exit(ee.ExitCode())
				}
				return err
			}},
	)
	return c
}

func restoreSecrets(e *client.Engine, force bool) error {
	rs, err := e.RestoreSecretFiles(force)
	for _, r := range rs {
		fmt.Printf("secret %-24s %s %s\n", r.Name, r.Action, r.Path)
	}
	return err
}

func tildePath(abs string) string {
	h, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(abs, h+string(filepath.Separator)) {
		return "~/" + strings.TrimPrefix(abs, h+string(filepath.Separator))
	}
	return abs
}

func passwdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "passwd",
		Short: "Change the master password (other devices keep working)",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			old, err := readPasswordVar("HEARTH_PASSWORD", "Current password: ", false)
			if err != nil {
				return err
			}
			nw, err := readPasswordVar("HEARTH_NEW_PASSWORD", "New password: ", true)
			if err != nil {
				return err
			}
			return report(e.ChangePassword(old, nw), "password changed")
		},
	}
}

func recoveryKeyCmd() *cobra.Command {
	c := &cobra.Command{Use: "recovery-key", Short: "Manage the account recovery key"}
	c.AddCommand(&cobra.Command{
		Use:   "rotate",
		Short: "Issue a new recovery key and invalidate the old one (e.g. it was shown to someone)",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			pw, err := readPassword("Master password: ", false)
			if err != nil {
				return err
			}
			rk, err := e.RotateRecoveryKey(pw)
			if err != nil {
				return err
			}
			fmt.Println("New recovery key (shown once, store it offline, e.g. in a password manager; do not paste it into chats):")
			fmt.Println("  " + rk)
			fmt.Println("The previous recovery key no longer works. Devices and data are unaffected.")
			return nil
		},
	})
	return c
}

func recoverCmd() *cobra.Command {
	var server, email, device, caCert string
	var insecure bool
	c := &cobra.Command{
		Use:   "recover",
		Short: "Forgot the password: set a new one with the recovery key (signs out all devices)",
		RunE: func(*cobra.Command, []string) error {
			if err := prepareServer(server, caCert, insecure); err != nil {
				return err
			}
			rk, err := readPasswordVar("HEARTH_RECOVERY_KEY", "Recovery key: ", false)
			if err != nil {
				return err
			}
			nw, err := readPasswordVar("HEARTH_NEW_PASSWORD", "New password: ", true)
			if err != nil {
				return err
			}
			if device == "" {
				device, _ = os.Hostname()
			}
			cfg, err := client.Recover(client.Home(), server, email, string(rk), nw, device)
			if err != nil {
				return err
			}
			fmt.Printf("password reset; all other devices were signed out and must log in again. This machine is logged in as %s.\n", cfg.Email)
			return nil
		},
	}
	c.Flags().StringVar(&server, "server", "", "server URL")
	c.Flags().StringVar(&email, "email", "", "account email")
	c.Flags().StringVar(&device, "device", "", "device name (default: hostname)")
	c.Flags().StringVar(&caCert, "ca-cert", "", "PEM certificate to trust for a self-signed server")
	c.Flags().BoolVar(&insecure, "insecure", false, "allow plain HTTP to a non-loopback server")
	c.MarkFlagRequired("server")
	c.MarkFlagRequired("email")
	return c
}
