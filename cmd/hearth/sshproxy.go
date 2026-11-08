package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/relay"
)

// stdio is the ProxyCommand side of a connection: what ssh writes goes to
// the connection and back.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return nil }

// sshProxyCmd is what ~/.ssh/config and `hearth ssh` use as ProxyCommand:
// it connects to the machine's sshd directly when that works and through the
// Hearth server (the relay) when it does not, so ssh, scp, rsync and the IDEs'
// Remote-SSH reach a machine that nobody can connect to. The host key check
// stays ssh's own, against the host keys the machines publish.
func sshProxyCmd() *cobra.Command {
	var device string
	var relayOnly bool
	c := &cobra.Command{
		Use:    "ssh-proxy --device <id> <host> <port>",
		Short:  "ProxyCommand for ssh: direct to the machine's sshd, else through the Hearth server",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, a []string) error {
			cmd.SilenceUsage = true
			if !relayOnly {
				if conn, err := net.DialTimeout("tcp", net.JoinHostPort(a[0], a[1]), 2*time.Second); err == nil {
					relay.Bridge(stdio{}, conn)
					return nil
				}
			}
			if device == "" {
				return fmt.Errorf("%s:%s does not answer", a[0], a[1])
			}
			e, err := engine()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			conn, err := e.DialRelay(ctx, device)
			if err != nil {
				if errors.Is(err, client.ErrNotInRelay) {
					return fmt.Errorf("%s:%s does not answer from here, and %v", a[0], a[1], err)
				}
				return err
			}
			// The relay reports why a machine cannot be served as the reason
			// of the connection's close; ssh shows our stderr.
			r := &reasonReader{Conn: conn}
			relay.Bridge(stdio{}, r)
			if err := r.reason(); err != nil {
				fmt.Fprintln(os.Stderr, "hearth:", client.RelayReason(err))
			}
			return nil
		},
	}
	c.Flags().StringVar(&device, "device", "", "the machine's device id (for the relay)")
	c.Flags().BoolVar(&relayOnly, "relay-only", false, "do not try a direct connection first")
	return c
}

// relayAgentCmd runs only the relay agent, in the foreground (the daemon runs
// it too): for trying the relay, or for an sshd that is not the one Hearth
// manages (--backend host:port).
func relayAgentCmd() *cobra.Command {
	var backend string
	c := &cobra.Command{
		Use:    "relay-agent",
		Short:  "Accept connections from your other machines through the Hearth server (what the daemon does by itself)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			var be client.RelayBackend
			if backend != "" {
				be = func(ctx context.Context) (net.Conn, error) {
					return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", backend)
				}
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			e.RunRelayAgent(ctx, be, func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) })
			return nil
		},
	}
	c.Flags().StringVar(&backend, "backend", "", "serve this host:port instead of the sshd Hearth runs")
	return c
}

// reasonReader remembers why the connection ended.
type reasonReader struct {
	net.Conn
	mu  sync.Mutex
	err error
}

func (r *reasonReader) Read(p []byte) (int, error) {
	n, err := r.Conn.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
	}
	return n, err
}

func (r *reasonReader) reason() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// sshRoute says how to reach a machine: the address that answers, extra ssh
// options, whether it can be reached, and whether that goes through the
// relay. When no address answers but the machine holds a relay connection
// (and the relay is not off here), ssh gets a ProxyCommand through the server.
func sshRoute(e *client.Engine, ep client.SSHEndpoint, wait time.Duration) (addr string, opts []string, ok, relayed bool) {
	if addr, ok = ep.ReachableAddress(wait); ok {
		return addr, nil, true, false
	}
	if !client.RelayDisabled(e.Home) {
		if ids, err := e.RelayDevices(); err == nil && slices.Contains(ids, ep.DeviceID) {
			if pc := relayProxyCommand(e.Home, ep.DeviceID, true); pc != "" {
				return addr, []string{"-o", "ProxyCommand=" + pc}, true, true
			}
		}
	}
	return addr, nil, false, false
}

// relayProxyCommand is the ProxyCommand line for a machine: HEARTH_HOME so
// the command finds this login from any directory, the full path of hearth,
// and ssh's own %h %p. "" when the relay is off or hearth cannot be found.
func relayProxyCommand(home, deviceID string, relayOnly bool) string {
	if client.RelayDisabled(home) || deviceID == "" {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		if p, err2 := exec.LookPath("hearth"); err2 == nil {
			exe = p
		} else {
			return ""
		}
	}
	// ssh runs a ProxyCommand as "exec <command>": the variable goes through env.
	cmd := fmt.Sprintf("env HEARTH_HOME=%s %s ssh-proxy --device %s", shQuote(home), shQuote(exe), shQuote(deviceID))
	if relayOnly {
		cmd += " --relay-only"
	}
	return cmd + " %h %p"
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
