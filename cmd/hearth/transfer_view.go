package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/datatool"
)

func transferListCmd() *cobra.Command {
	var all, asJSON bool
	var status, keyword string
	var n int
	c := &cobra.Command{
		Use:   "list",
		Short: "Tasks submitted from this machine (--all: every task on the service)",
		Long: "The service does not tell users apart, so by default only the tasks this machine submitted are shown\n" +
			"(their current state is fetched). --all lists the service's tasks, whoever created them.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			env, err := loadTransferEnv()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			var tasks []datatool.Task
			if all {
				if tasks, err = env.cl.ListTasks(ctx, datatool.TaskQuery{Status: status, Keyword: keyword, PageSize: n}); err != nil {
					return err
				}
			} else {
				recs, err := env.store.List()
				if err != nil {
					return err
				}
				for _, r := range recs {
					if len(tasks) >= n {
						break
					}
					t, err := env.cl.GetTask(ctx, r.ID)
					if err != nil {
						t = &datatool.Task{ID: r.ID, Name: r.Name, Src: r.Src, Dst: r.Dst, Status: "unknown", Message: err.Error()}
					}
					if status != "" && t.Status != status || keyword != "" && !strings.Contains(t.Name+" "+t.ID, keyword) {
						continue
					}
					tasks = append(tasks, *t)
				}
			}
			if asJSON {
				return printJSON(tasks)
			}
			if len(tasks) == 0 {
				if all {
					fmt.Println("no tasks")
				} else {
					fmt.Println("no tasks submitted from this machine (--all lists the service's tasks)")
				}
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATE\tPROGRESS\tNAME\tFROM -> TO")
			for _, t := range tasks {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s %s -> %s %s\n", t.ID, t.Status, progressShort(t), t.Name, t.Src.Storage, t.Src.Path, t.Dst.Storage, t.Dst.Path)
			}
			return w.Flush()
		},
	}
	c.Flags().BoolVar(&all, "all", false, "every task on the service, not only this machine's")
	c.Flags().StringVar(&status, "status", "", "only this state (pending, serving, running, cleaning, succeeded, failed, canceling, canceled)")
	c.Flags().StringVar(&keyword, "keyword", "", "only tasks whose name or id contains this")
	c.Flags().IntVarP(&n, "limit", "n", 20, "at most this many tasks")
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func transferStatusCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "status <task-id>",
		Short: "State, progress and error of one task",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			env, err := loadTransferEnv()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			t, err := env.cl.GetTask(ctx, a[0])
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(t)
			}
			printTask(t)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func transferCancelCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "cancel <task-id>",
		Short: "Cancel a task (only your own, unless --force)",
		Long: "Asks the service to stop the task. The service does not tell users apart, so Hearth only cancels tasks\n" +
			"this machine submitted; --force cancels any task id, which can stop somebody else's copy.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			env, err := loadTransferEnv()
			if err != nil {
				return err
			}
			if !env.store.Has(a[0]) && !force {
				return errors.New(a[0] + " was not submitted from this machine; --force cancels it anyway (it may be somebody else's task)")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := env.cl.CancelTask(ctx, a[0]); err != nil {
				return err
			}
			state := "requested"
			if t, err := env.cl.GetTask(ctx, a[0]); err == nil {
				state = t.Status
			}
			fmt.Printf("cancel sent for %s (state: %s)\n", a[0], state)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "cancel a task this machine did not submit")
	return c
}

func printTask(t *datatool.Task) {
	fmt.Printf("task:      %s\n", t.ID)
	fmt.Printf("name:      %s\n", t.Name)
	fmt.Printf("state:     %s\n", t.Status)
	fmt.Printf("source:    %s %s\n", t.Src.Storage, t.Src.Path)
	fmt.Printf("dest:      %s %s\n", t.Dst.Storage, t.Dst.Path)
	if p := progressLong(t.Progress); p != "" {
		fmt.Printf("progress:  %s\n", p)
	}
	if t.Progress.TotalObjects > 0 {
		fmt.Printf("objects:   %d (%d transferring)\n", t.Progress.TotalObjects, t.Progress.Transfers)
	}
	if t.Progress.Errors > 0 {
		fmt.Printf("errors:    %d\n", t.Progress.Errors)
	}
	if t.Message != "" {
		fmt.Printf("message:   %s\n", t.Message)
	}
	if t.ErrorCode != "" {
		fmt.Printf("code:      %s\n", t.ErrorCode)
	}
	if t.Options.DryRun {
		fmt.Println("dry run:   yes")
	}
	fmt.Printf("created:   %s\n", localTime(&t.CreatedAt))
	if t.StartedAt != nil {
		fmt.Printf("started:   %s\n", localTime(t.StartedAt))
	}
	if t.FinishedAt != nil {
		fmt.Printf("finished:  %s (took %s)\n", localTime(t.FinishedAt), t.FinishedAt.Sub(startOf(t)).Round(time.Second))
	}
	if t.ServeAddr != "" {
		fmt.Printf("serve:     %s\n", t.ServeAddr)
	}
}

func startOf(t *datatool.Task) time.Time {
	if t.StartedAt != nil {
		return *t.StartedAt
	}
	return t.CreatedAt
}

func localTime(t *time.Time) string {
	if t == nil || t.IsZero() || t.Year() <= 1 {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// progressShort: "25%" or "-".
func progressShort(t datatool.Task) string {
	if p := t.Progress; p.TotalBytes > 0 {
		return fmt.Sprintf("%d%%", min(100, p.Bytes*100/p.TotalBytes))
	}
	return "-"
}

// progressLong: "230 GiB / 45 TiB (0%) · 433 MiB/s · ETA 30h4m21s".
func progressLong(p datatool.Progress) string {
	if p.TotalBytes <= 0 && p.Bytes <= 0 {
		return ""
	}
	s := bytesString(p.Bytes)
	if p.TotalBytes > 0 {
		s += fmt.Sprintf(" / %s (%d%%)", bytesString(p.TotalBytes), min(100, p.Bytes*100/p.TotalBytes))
	}
	if p.SpeedBps > 0 {
		s += " · " + bytesString(int64(p.SpeedBps)) + "/s"
	}
	if p.ETASeconds > 0 {
		s += " · ETA " + (time.Duration(p.ETASeconds) * time.Second).String()
	}
	return s
}
