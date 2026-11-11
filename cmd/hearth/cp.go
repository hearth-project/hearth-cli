package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/xfer"
)

// exitUnreachable: `hearth cp` on a machine could not reach the other one
// (the caller then tries from the other side).
const exitUnreachable = 3

type endpoint struct {
	machine string // "" = this machine
	path    string
}

// parseEndpoint reads [machine:]path. A colon makes it remote only when
// the part before it names one of the user's machines.
func parseEndpoint(e *client.Engine, eps []client.SSHEndpoint, s string) (endpoint, error) {
	m, p, ok := strings.Cut(s, ":")
	if !ok || m == "" || strings.ContainsAny(m, "/\\") || strings.HasPrefix(s, ".") {
		return endpoint{path: s}, nil
	}
	if p == "" {
		p = "."
	}
	if _, err := findSSHTarget(e, eps, m); err != nil {
		if _, statErr := os.Stat(s); statErr == nil {
			return endpoint{path: s}, nil // a local file with a colon in its name
		}
		return endpoint{}, err
	}
	if isThisMachine(e, m) {
		return endpoint{path: p}, nil
	}
	return endpoint{machine: m, path: p}, nil
}

func cpCmd() *cobra.Command {
	var streams int
	var compress string
	var noClobber, force, quiet, noShared bool
	c := &cobra.Command{
		Use:   "cp <[machine:]source> <[machine:]target>",
		Short: "Copy files or directories between your machines, directly (parallel connections, resumable)",
		Long: "Copies a file or a directory to, from or between your machines. The control channel goes over\n" +
			"Hearth's SSH; the data goes straight between the two machines over parallel TCP connections,\n" +
			"encrypted with a key that only went through SSH (never through the Hearth server). Large files\n" +
			"travel in chunks over every connection at once; an interrupted copy resumes when run again; files\n" +
			"whose size and time already match are skipped. When the machines cannot connect directly in either\n" +
			"direction, the data goes through SSH port forwards. When both see the target on a shared disk,\n" +
			"it is copied there directly.\n\n" +
			"  hearth cp ./ckpt gpu-a:/data/            push into /data/ckpt on gpu-a\n" +
			"  hearth cp gpu-a:/data/ckpt .              pull\n" +
			"  hearth cp gpu-a:/data/ckpt gpu-b:/data/   between two machines (straight from gpu-a to gpu-b)\n\n" +
			"A target that is an existing directory or ends in / receives the source inside it (like cp -r).",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, a []string) error {
			switch compress {
			case "auto", "on", "off":
			default:
				return errors.New("--compress takes auto, on or off")
			}
			opts := xfer.Options{Streams: streams, NoClobber: noClobber, Force: force}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if !strings.Contains(a[0], ":") && !strings.Contains(a[1], ":") {
				return localCopy(ctx, a[0], a[1], opts, quiet)
			}
			ui := newCPUI(quiet)
			e, err := engine()
			if err != nil {
				return err
			}
			eps, err := e.SSHEndpoints()
			if err != nil {
				return err
			}
			src, err := parseEndpoint(e, eps, a[0])
			if err != nil {
				return err
			}
			dst, err := parseEndpoint(e, eps, a[1])
			if err != nil {
				return err
			}
			switch {
			case src.machine == "" && dst.machine == "":
				return localCopy(ctx, src.path, dst.path, opts, quiet)
			case src.machine != "" && dst.machine != "":
				return thirdParty(src, dst, cmd)
			}
			cfg := xfer.ClientConfig{Opts: opts, Compress: compress, NoShared: noShared,
				Progress: ui.progress, Info: ui.info, Route: ui.route}
			send := dst.machine != ""
			machine, local, remote := dst.machine, src.path, dst.path
			if !send {
				machine, local, remote = src.machine, dst.path, src.path
			}
			ui.header(a[0], a[1])
			var res *xfer.Result
			for attempt := 1; ; attempt++ {
				ui.connecting("connecting to " + machine)
				res, err = remoteCopy(ctx, e, eps, machine, send, local, remote, cfg)
				if err == nil || ctx.Err() != nil || attempt == 3 || errors.As(err, new(*unreachableError)) {
					break
				}
				ui.line("retry", fmt.Sprintf("%v; trying again (it resumes)", err))
				time.Sleep(time.Second)
			}
			ui.stop()
			if err != nil && ctx.Err() != nil {
				return errors.New("interrupted; run the same command again to resume")
			}
			if err != nil {
				var ue *unreachableError
				if errors.As(err, &ue) {
					fmt.Fprintln(os.Stderr, "error:", err)
					os.Exit(exitUnreachable)
				}
				return err
			}
			where := res.Plan.Target
			if send {
				where = machine + ":" + where
			}
			ui.done(res, where)
			return nil
		},
	}
	c.Flags().IntVarP(&streams, "streams", "j", 0, "parallel data connections (default: 16 nearby, up to 64 across regions)")
	c.Flags().StringVar(&compress, "compress", "auto", "zstd per chunk, kept where it helps: auto (across regions), on, off")
	c.Flags().BoolVarP(&noClobber, "no-clobber", "n", false, "keep files that already exist at the target")
	c.Flags().BoolVar(&force, "force", false, "copy files even when their size and time match")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "no progress")
	c.Flags().BoolVar(&noShared, "no-shared", false, "do not copy on a disk both machines see; always send the data")
	_ = c.Flags().MarkHidden("no-shared")
	return c
}

type unreachableError struct{ err error }

func (u *unreachableError) Error() string { return u.err.Error() }

func localCopy(ctx context.Context, src, dst string, o xfer.Options, quiet bool) error {
	ui := newCPUI(quiet)
	t0 := time.Now()
	p, err := xfer.Local(ctx, src, dst, o, ui.progress)
	ui.stop()
	if err != nil {
		return err
	}
	ui.done(&xfer.Result{Plan: p, Bytes: p.Bytes, Took: time.Since(t0), Streams: max(o.Streams, 16), Via: "local"}, p.Target)
	return nil
}

// remoteCopy runs `hearth xfer serve` on the machine over hearth ssh and
// drives the copy from here.
func remoteCopy(ctx context.Context, e *client.Engine, eps []client.SSHEndpoint, machine string, send bool, local, remote string, cfg xfer.ClientConfig) (*xfer.Result, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// Unreachable machines fail at once (ssh would wait for its own timeout),
	// so a copy between two machines can try from the other side.
	if ep, err := findSSHTarget(e, eps, machine); err != nil {
		return nil, &unreachableError{err}
	} else if _, _, ok, _ := sshRoute(e, ep, 5*time.Second); !ok {
		return nil, &unreachableError{fmt.Errorf("%s does not answer at %s (SSH, port %d) from here", machine, strings.Join(ep.Addrs(), ", "), ep.Port)}
	}
	bin := "hearth"
	if b := os.Getenv("HEARTH_XFER_REMOTE_BIN"); b != "" {
		bin = shellQuote(b) // a test build on the target
	}
	cmd := exec.CommandContext(ctx, self, "ssh", machine, "--", "sh", "-c", shellQuote(`PATH="$HOME/.local/bin:$PATH"; exec `+bin+` xfer serve`))
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		in.Close()
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
		}
	}()
	var tunnels []*exec.Cmd
	defer func() {
		for _, t := range tunnels {
			t.Process.Kill()
			t.Wait()
		}
	}()
	cfg.Tunnel = func(port, n int) ([]string, error) {
		ep, err := findSSHTarget(e, eps, machine)
		if err != nil {
			return nil, err
		}
		addr, route, _, _ := sshRoute(e, ep, 3*time.Second)
		var locals []string
		for i := 0; i < n; i++ {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			lp := l.Addr().(*net.TCPAddr).Port
			l.Close()
			argv := append(append(sshArgs(e.Home, ep), route...), "-N", "-o", "ExitOnForwardFailure=yes",
				"-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", lp, port), ep.User+"@"+addr)
			t := exec.Command("ssh", argv...)
			if err := t.Start(); err != nil {
				return nil, err
			}
			tunnels = append(tunnels, t)
			locals = append(locals, net.JoinHostPort("127.0.0.1", strconv.Itoa(lp)))
		}
		for _, a := range locals { // wait until the forwards listen
			for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
				if c, err := net.DialTimeout("tcp", a, 300*time.Millisecond); err == nil {
					c.Close()
					break
				}
			}
		}
		return locals, nil
	}
	res, err := xfer.Client(ctx, xfer.NewCtrl(out, in), send, local, remote, cfg)
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		switch {
		case strings.Contains(msg, "unknown command \"xfer\""):
			return nil, &unreachableError{fmt.Errorf("hearth on %s is too old for hearth cp: hearth self-update there", machine)}
		case err.Error() == "the other machine ended the transfer" && msg != "":
			msg = strings.TrimPrefix(msg, "error: ")
			if strings.Contains(msg, "does not answer") || strings.Contains(msg, "Connection refused") || strings.Contains(msg, "no machine") {
				return nil, &unreachableError{fmt.Errorf("%s: %s", machine, msg)}
			}
			return nil, fmt.Errorf("%s: %s", machine, msg)
		}
		return nil, err
	}
	return res, nil
}

// thirdParty copies between two other machines: run on the source (it
// pushes); when it cannot reach the target, on the target (it pulls).
func thirdParty(src, dst endpoint, cmd *cobra.Command) error {
	var flags []string
	cmd.Flags().Visit(func(f *pflag.Flag) { flags = append(flags, "--"+f.Name+"="+f.Value.String()) })
	run := func(on, a, b string) (int, error) {
		args := []string{"cp"}
		args = append(args, flags...)
		args = append(args, a, b)
		q := make([]string, len(args))
		for i, x := range args {
			q[i] = shellQuote(x)
		}
		self, _ := os.Executable()
		run := "exec hearth"
		if b := os.Getenv("HEARTH_XFER_REMOTE_BIN"); b != "" {
			run = "HEARTH_XFER_REMOTE_BIN=" + shellQuote(b) + " exec " + shellQuote(b) // a test build on both
		}
		c := exec.Command(self, "ssh", on, "--", "sh", "-c", shellQuote(`PATH="$HOME/.local/bin:$PATH"; `+run+` `+strings.Join(q, " ")))
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		err := c.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), err
		}
		return 0, err
	}
	fmt.Fprintf(os.Stderr, "hearth cp: on %s, sending to %s\n", src.machine, dst.machine)
	code, err := run(src.machine, src.path, dst.machine+":"+dst.path)
	if code != exitUnreachable {
		return quietExit(err)
	}
	fmt.Fprintf(os.Stderr, "hearth cp: %s cannot reach %s; on %s, fetching from %s instead\n", src.machine, dst.machine, dst.machine, src.machine)
	_, err = run(dst.machine, src.machine+":"+src.path, dst.path)
	return quietExit(err)
}

// quietExit: the remote command printed its own error.
func quietExit(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	return err
}

// cpTTY: progress and spinners only in a terminal.
var cpTTY = term.IsTerminal(int(os.Stderr.Fd()))

func xferCmd() *cobra.Command {
	c := &cobra.Command{Use: "xfer", Hidden: true, Short: "The other end of hearth cp"}
	c.AddCommand(&cobra.Command{Use: "serve", Short: "Serve one copy on stdin/stdout (started by hearth cp over hearth ssh)", RunE: func(*cobra.Command, []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer stop()
		return xfer.Serve(ctx, xfer.NewCtrl(os.Stdin, os.Stdout))
	}})
	return c
}
