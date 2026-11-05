package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/hearth-project/hearth-cli/internal/datatool"
)

func transferCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "transfer",
		Short: "Copy large directories between storage systems with a DataTool-compatible service (asynchronous, server-side)",
		Long: "Submits copy tasks to a transfer service that moves a directory from one storage system to another\n" +
			"on its own side (e.g. between two clusters' shared file systems), tracks them, and cancels them.\n" +
			"The data does not pass through this machine or the Hearth server, and it is not encrypted or versioned\n" +
			"by Hearth: use `hearth cp` or the normal sync for personal state, small files and anything that must\n" +
			"stay end-to-end encrypted. The server admin provides the service address, token and path rules\n" +
			"(transfer.json in the server's data directory); [transfer] in config.toml overrides them\n" +
			"(see docs/en/usage.md).\n\n" +
			"  hearth transfer meta                      storages, allowed directions, limits\n" +
			"  hearth transfer submit --from a@one:/data/ckpt --to b@two:/data/ckpt\n" +
			"  hearth transfer list | status <id> | cancel <id>",
	}
	c.AddCommand(transferMetaCmd(), transferSubmitCmd(), transferListCmd(), transferStatusCmd(), transferCancelCmd())
	return c
}

func transferMetaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "meta",
		Short: "What the service can copy: storages, allowed directions, path hints, defaults and limits",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			env, err := loadTransferEnv()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			m, err := env.cl.Meta(ctx)
			if err != nil {
				return err
			}
			fmt.Println("directions:")
			for _, p := range m.ValidPairs {
				fmt.Printf("  %s -> %s\n", p.Src, p.Dst)
			}
			fmt.Println("paths:")
			for _, s := range m.Storages {
				fmt.Printf("  %-14s %s\n", s, m.PathHints[s])
			}
			d, l := m.DefaultOptions, m.Limits
			fmt.Printf("defaults: transfers %d (max %d), checkers %d (max %d)\n", d.Transfers, l.TransfersMax, d.Checkers, l.CheckersMax)
			fmt.Printf("limits:   name %d characters, path %d characters\n", l.NameMaxLen, l.PathMaxLen)
			if m.DestinationEmptyHint != "" {
				fmt.Println("note:    ", m.DestinationEmptyHint)
			}
			return nil
		},
	}
}

func transferSubmitCmd() *cobra.Command {
	var from, to, name string
	var transfers, checkers int
	var dryRun, yes bool
	c := &cobra.Command{
		Use:   "submit --from STORAGE:PATH --to STORAGE:PATH",
		Short: "Submit a copy task (shows the plan and asks first)",
		Long: "Copies the source directory to the destination on the service's side. STORAGE is a name from\n" +
			"`hearth transfer meta`; PATH is the path on that storage. A path under a prefix listed in\n" +
			"[transfer.paths.\"STORAGE\"] of config.toml is translated first (so a path as seen on this machine\n" +
			"works); a URL such as afs://... and any other absolute path are sent as given.\n\n" +
			"The destination directory must be empty. The plan is printed and you confirm it; without a terminal\n" +
			"pass --yes. --dry-run asks the service for a dry run. AFS needs a UGI: [transfer] afs_ugi_secret.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if from == "" || to == "" {
				return errors.New("--from and --to are required (STORAGE:PATH)")
			}
			env, err := loadTransferEnv()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			meta, err := env.cl.Meta(ctx)
			if err != nil {
				return err
			}
			src, srcMapped, err := env.parseStoragePath(meta.Storages, from)
			if err != nil {
				return fmt.Errorf("--from: %w", err)
			}
			dst, dstMapped, err := env.parseStoragePath(meta.Storages, to)
			if err != nil {
				return fmt.Errorf("--to: %w", err)
			}
			opts := meta.DefaultOptions
			opts.DryRun = dryRun
			if cmd.Flags().Changed("transfers") {
				opts.Transfers = transfers
			}
			if cmd.Flags().Changed("checkers") {
				opts.Checkers = checkers
			}
			if name == "" {
				name = defaultTransferName(dst.Path, meta.Limits.NameMaxLen)
			}
			req := datatool.CreateRequest{Name: name, Src: src, Dst: dst, Options: opts}
			if datatool.NeedsAFSUGI(src.Storage, dst.Storage) {
				if env.cfg.AFSUGISecret == "" {
					return errors.New("an afs storage needs a UGI: store it with `hearth secret set afs_ugi` and set afs_ugi_secret = \"afs_ugi\" under [transfer]")
				}
				ugi, err := transferSecret(env.cfg.AFSUGISecret, "afs_ugi_secret")
				if err != nil {
					return err
				}
				req.Secrets = &datatool.Secrets{AFSUGI: ugi}
			}
			if err := datatool.Validate(meta, req); err != nil {
				return err
			}
			printPlan(req, srcMapped, dstMapped, meta.DestinationEmptyHint)
			if !yes {
				if err := confirmTransfer(); err != nil {
					return err
				}
			}
			task, err := env.cl.CreateTask(ctx, req)
			if err != nil {
				return err
			}
			host, _ := os.Hostname()
			if err := env.store.Add(datatool.Record{ID: task.ID, Name: req.Name, Src: req.Src, Dst: req.Dst,
				DryRun: opts.DryRun, SubmittedAt: time.Now(), Host: host}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: the task is submitted but could not be recorded here (%v); note its id\n", err)
			}
			fmt.Println("transfer task submitted")
			fmt.Printf("task:    %s\n", task.ID)
			fmt.Printf("state:   %s\n", orDash(task.Status))
			fmt.Printf("follow:  hearth transfer status %s\n", task.ID)
			return nil
		},
	}
	c.Flags().StringVar(&from, "from", "", "source, STORAGE:PATH")
	c.Flags().StringVar(&to, "to", "", "destination, STORAGE:PATH (an empty directory)")
	c.Flags().StringVar(&name, "name", "", "task name (default: hearth-<destination directory>-<date>)")
	c.Flags().IntVar(&transfers, "transfers", 0, "parallel file transfers (default: the service's, at most its limit)")
	c.Flags().IntVar(&checkers, "checkers", 0, "parallel checkers (default: the service's, at most its limit)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "ask the service for a dry run")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask; you confirm the destination directory is empty")
	return c
}

func printPlan(r datatool.CreateRequest, srcMapped, dstMapped bool, emptyHint string) {
	note := func(mapped bool) string {
		if mapped {
			return "  (translated by a [transfer.paths] rule)"
		}
		return ""
	}
	fmt.Println("plan:")
	fmt.Printf("  name:         %s\n", r.Name)
	fmt.Printf("  source:       %s %s%s\n", r.Src.Storage, r.Src.Path, note(srcMapped))
	fmt.Printf("  destination:  %s %s%s\n", r.Dst.Storage, r.Dst.Path, note(dstMapped))
	fmt.Printf("  transfers:    %d   checkers: %d\n", r.Options.Transfers, r.Options.Checkers)
	if r.Options.DryRun {
		fmt.Println("  dry run:      yes")
	}
	if r.Secrets != nil {
		fmt.Println("  afs ugi:      from your secrets (not shown)")
	}
	if emptyHint == "" {
		emptyHint = "the destination directory must be empty"
	}
	fmt.Println("  note:        ", emptyHint)
}

func confirmTransfer() error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("not a terminal: pass --yes to confirm the plan (the destination directory must be empty)")
	}
	fmt.Fprint(os.Stderr, "Submit this task? [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		return errors.New("not submitted")
	}
	return nil
}

// defaultTransferName: hearth-<last directory of the destination>-<date>.
func defaultTransferName(dstPath string, max int) string {
	base := path.Base(strings.TrimRight(dstPath, "/"))
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	n := "hearth"
	if s := strings.Trim(b.String(), "-."); s != "" {
		n += "-" + s
	}
	n += "-" + time.Now().Format("20060102")
	if r := []rune(n); max > 0 && len(r) > max {
		n = string(r[:max])
	}
	return n
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
