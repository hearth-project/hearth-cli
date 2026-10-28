package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/logx"
)

// A small supervisor keeps the server running without systemd (containers):
// `hearth server start` detaches a supervisor that runs `hearth server` as a
// child, restarts it when it exits or stops answering /healthz, and writes
// its output to a size-rotated log.

const (
	logMax        = 10 << 20 // bytes per server.log file
	logKeep       = 5        // rotated files kept
	probeFailures = 3        // consecutive failed health checks before a restart
	stableAfter   = 5 * time.Minute
)

// probeEvery is how often the supervisor checks /healthz ($HEARTH_PROBE_EVERY).
var probeEvery = func() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("HEARTH_PROBE_EVERY")); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}()

type supervisorState struct {
	SupervisorPID int       `json:"supervisor_pid"`
	ServerPID     int       `json:"server_pid"`
	Started       time.Time `json:"started"`      // supervisor
	ServerSince   time.Time `json:"server_since"` // current child
	Restarts      int       `json:"restarts"`     // since the supervisor started
	LastExit      string    `json:"last_exit,omitempty"`
	LastExitAt    time.Time `json:"last_exit_at,omitempty"`
	Args          []string  `json:"args"`
}

func supPaths(data string) (lock, state, logf string) {
	return filepath.Join(data, "supervisor.lock"), filepath.Join(data, "supervisor.json"), filepath.Join(data, "server.log")
}

// urlFile is written by the server once it listens; the supervisor probes it.
func urlFile(data string) string { return filepath.Join(data, "server.url") }

// dataDirOf finds --data in raw server arguments (default $HEARTH_DATA or ./hearth-data).
func dataDirOf(args []string) string {
	d := envOr("HEARTH_DATA", "./hearth-data")
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, "--data="); ok {
			d = v
		} else if a == "--data" && i+1 < len(args) {
			d = args[i+1]
		}
	}
	abs, err := filepath.Abs(d)
	if err != nil {
		return d
	}
	return abs
}

// supervisorRunning returns the supervisor pid holding the lock (0 = none).
func supervisorRunning(data string) int {
	lock, state, _ := supPaths(data)
	f, err := os.OpenFile(lock, os.O_RDWR, 0o600)
	if err != nil {
		return 0
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return 0
	}
	var st supervisorState
	if b, err := os.ReadFile(state); err == nil && json.Unmarshal(b, &st) == nil {
		return st.SupervisorPID
	}
	return -1
}

func readState(data string) (supervisorState, error) {
	var st supervisorState
	_, state, _ := supPaths(data)
	b, err := os.ReadFile(state)
	if err == nil {
		err = json.Unmarshal(b, &st)
	}
	return st, err
}

func serverStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start [server flags]",
		Short: "Run the server in the background under a supervisor (restarts it if it exits or hangs; rotated log)",
		Long: "Takes the same flags as `hearth server` (e.g. --data, --addr, --public-url, --db). The supervisor holds\n" +
			"<data>/supervisor.lock, logs to <data>/server.log (10 MiB x 5) and restarts the server with backoff when it\n" +
			"exits, or when /healthz fails 3 times in a row. Running it again is a no-op, so it is safe in cron or a\n" +
			"container entrypoint. Without systemd, add it to the entrypoint or `@reboot` cron to survive reboots.",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
				return cmd.Help()
			}
			data := dataDirOf(args)
			if pid := supervisorRunning(data); pid != 0 {
				fmt.Printf("already running (supervisor pid %d); hearth server status --data %s\n", pid, data)
				return nil
			}
			if err := os.MkdirAll(data, 0o700); err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			_, _, logPath := supPaths(data)
			lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			defer lf.Close()
			devnull, _ := os.Open(os.DevNull)
			defer devnull.Close()
			c := exec.Command(exe, append([]string{"server", "supervise", "--"}, args...)...)
			c.Stdin, c.Stdout, c.Stderr = devnull, lf, lf // only until the supervisor opens its rotating log
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := c.Start(); err != nil {
				return err
			}
			c.Process.Release()
			for i := 0; i < 100; i++ {
				time.Sleep(100 * time.Millisecond)
				if st, err := readState(data); err == nil && st.ServerPID != 0 && supervisorRunning(data) != 0 {
					if u, err := os.ReadFile(urlFile(data)); err == nil && healthy(strings.TrimSpace(string(u))) {
						fmt.Printf("hearth server running at %s (supervisor %d, server %d); log %s\n", strings.TrimSpace(string(u)), st.SupervisorPID, st.ServerPID, logPath)
						if cmd := lastInstallCommand(logPath); cmd != "" {
							fmt.Printf("new machines install and restore with (download page: %s/):\n  %s\n", strings.TrimSpace(string(u)), cmd)
						}
						return nil
					}
				}
			}
			return fmt.Errorf("server did not become healthy within 10 s; see %s", logPath)
		},
	}
}

// stopSupervisor stops the supervisor of data and waits for it.
func stopSupervisor(data string) error {
	pid := supervisorRunning(data)
	if pid == 0 {
		fmt.Println("not running")
		return nil
	}
	if pid < 0 {
		return errors.New("supervisor is running but its state file is missing")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for i := 0; i < 300; i++ {
		if supervisorRunning(data) == 0 {
			fmt.Println("stopped")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("supervisor %d did not stop within 30 s", pid)
}

func serverStopCmd() *cobra.Command {
	var data string
	c := &cobra.Command{Use: "stop", Short: "Stop the supervised server (graceful)", RunE: func(*cobra.Command, []string) error {
		return stopSupervisor(dataDirOf([]string{"--data", data}))
	}}
	c.Flags().StringVar(&data, "data", envOr("HEARTH_DATA", "./hearth-data"), "server data directory")
	return c
}

func serverRestartCmd() *cobra.Command {
	var data string
	c := &cobra.Command{Use: "restart", Short: "Restart the supervised server with the flags it was started with", RunE: func(cmd *cobra.Command, _ []string) error {
		data = dataDirOf([]string{"--data", data})
		st, err := readState(data)
		if err != nil {
			return fmt.Errorf("no supervisor state in %s: start it with hearth server start", data)
		}
		if err := stopSupervisor(data); err != nil {
			return err
		}
		return serverStartCmd().RunE(cmd, st.Args)
	}}
	c.Flags().StringVar(&data, "data", envOr("HEARTH_DATA", "./hearth-data"), "server data directory")
	return c
}

func serverStatusCmd() *cobra.Command {
	var data string
	c := &cobra.Command{Use: "status", Short: "Show the supervised server: pids, uptime, restarts, health", RunE: func(*cobra.Command, []string) error {
		data = dataDirOf([]string{"--data", data})
		pid := supervisorRunning(data)
		st, _ := readState(data)
		_, _, logPath := supPaths(data)
		if pid == 0 {
			fmt.Printf("not running (start: hearth server start --data %s ...)\n", data)
			if st.LastExit != "" {
				fmt.Printf("last server exit: %s at %s\n", st.LastExit, st.LastExitAt.Format(time.DateTime))
			}
			return errors.New("not running")
		}
		u, _ := os.ReadFile(urlFile(data))
		url := strings.TrimSpace(string(u))
		h := "healthy"
		if !healthy(url) {
			h = "NOT RESPONDING"
		}
		fmt.Printf("server:     %s %s (pid %d, up %s)\n", url, h, st.ServerPID, time.Since(st.ServerSince).Round(time.Second))
		fmt.Printf("supervisor: pid %d, up %s, %d restart(s)\n", st.SupervisorPID, time.Since(st.Started).Round(time.Second), st.Restarts)
		if st.LastExit != "" {
			fmt.Printf("last exit:  %s at %s\n", st.LastExit, st.LastExitAt.Format(time.DateTime))
		}
		fmt.Printf("log:        %s (rotated at %d MiB, %d kept)\n", logPath, logMax>>20, logKeep)
		return nil
	}}
	c.Flags().StringVar(&data, "data", envOr("HEARTH_DATA", "./hearth-data"), "server data directory")
	return c
}

// healthy probes /healthz (self-signed certificates are fine: it is our own
// server on this host).
func healthy(url string) bool {
	if url == "" {
		return false
	}
	hc := &http.Client{Timeout: min(5*time.Second, probeEvery), Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := hc.Get(url + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// superviseCmd is the supervisor process itself (started by `start`).
func superviseCmd() *cobra.Command {
	return &cobra.Command{
		Use: "supervise", Hidden: true, DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			return supervise(dataDirOf(args), args)
		},
	}
}

func supervise(data string, args []string) error {
	lock, statePath, logPath := supPaths(data)
	lf, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another supervisor is running for " + data)
	}
	out, err := logx.NewRotating(logPath, logMax, logKeep)
	if err != nil {
		return err
	}
	defer out.Close()
	logger := log.New(out, "", log.LstdFlags)
	exe, _ := os.Executable()
	st := supervisorState{SupervisorPID: os.Getpid(), Started: time.Now(), Args: args}
	save := func() {
		b, _ := json.MarshalIndent(st, "", "  ")
		os.WriteFile(statePath+".tmp", b, 0o600)
		os.Rename(statePath+".tmp", statePath)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	backoff := time.Second
	logger.Printf("supervisor started (pid %d)", os.Getpid())
	for {
		os.Remove(urlFile(data))
		child := exec.Command(exe, append([]string{"server"}, args...)...)
		child.Stdout, child.Stderr = out, out
		child.Env = append(os.Environ(), "HEARTH_SUPERVISED=1")
		if err := child.Start(); err != nil {
			logger.Printf("start server: %v", err)
			return err
		}
		st.ServerPID, st.ServerSince = child.Process.Pid, time.Now()
		save()
		exited := make(chan error, 1)
		go func() { exited <- child.Wait() }()
		probe := time.NewTicker(probeEvery)
		fails := 0
		var why string
	wait:
		for {
			select {
			case s := <-sig:
				probe.Stop()
				logger.Printf("supervisor: %v, stopping server", s)
				child.Process.Signal(syscall.SIGTERM)
				select {
				case <-exited:
				case <-time.After(20 * time.Second):
					child.Process.Kill()
					<-exited
				}
				st.ServerPID = 0
				save()
				os.Remove(urlFile(data))
				return nil
			case err := <-exited:
				why = fmt.Sprintf("exited: %v", err)
				if err == nil {
					why = "exited"
				}
				break wait
			case <-probe.C:
				u, _ := os.ReadFile(urlFile(data))
				if healthy(strings.TrimSpace(string(u))) {
					fails = 0
					continue
				}
				if time.Since(st.ServerSince) < probeEvery {
					continue // still starting
				}
				if fails++; fails >= probeFailures {
					why = fmt.Sprintf("not answering /healthz %d times, killed", fails)
					child.Process.Kill()
					<-exited
					break wait
				}
			}
		}
		probe.Stop()
		ran := time.Since(st.ServerSince)
		if ran > stableAfter {
			backoff = time.Second
		}
		st.Restarts++
		st.LastExit, st.LastExitAt, st.ServerPID = why, time.Now(), 0
		save()
		logger.Printf("supervisor: server %s after %s; restarting in %s", why, ran.Round(time.Second), backoff)
		select {
		case <-time.After(backoff):
		case s := <-sig:
			logger.Printf("supervisor: %v", s)
			return nil
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func serverInstallServiceCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "install-service [server flags]",
		Short:              "Print a systemd unit for these server flags (when systemd runs; containers: use `hearth server start`)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
				return cmd.Help()
			}
			exe, _ := os.Executable()
			q := make([]string, len(args))
			for i, a := range args {
				q[i] = strconv.Quote(a)
			}
			fmt.Printf(`# Save as /etc/systemd/system/hearth.service, then: systemctl daemon-reload && systemctl enable --now hearth
[Unit]
Description=Hearth server
After=network-online.target
Wants=network-online.target

[Service]
WorkingDirectory=%s
ExecStart=%s server %s
Restart=always
RestartSec=3
WatchdogSec=0
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`, must(os.Getwd()), exe, strings.Join(q, " "))
			if _, err := os.Stat("/run/systemd/system"); err != nil {
				fmt.Fprintln(os.Stderr, "note: systemd is not running here (container?). Use `hearth server start ...` and add it to the container entrypoint or an @reboot cron job.")
			}
			return nil
		},
	}
}

func must(s string, _ error) string { return s }

// lastInstallCommand finds the install one-liner the server logged at startup.
func lastInstallCommand(logPath string) string {
	b, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	cmd := ""
	for _, l := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "curl ") && strings.Contains(t, "/install.sh") {
			cmd = t
		}
	}
	return cmd
}
