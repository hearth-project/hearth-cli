package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSSHD answers every connection with a banner and then echoes.
func fakeSSHD(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.Write([]byte("SSH-2.0-fake\r\n"))
				io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func startAgent(t *testing.T, m *machine, backend RelayBackend) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go m.RunRelayAgent(ctx, backend, t.Logf)
	return cancel
}

// dialWhenUp waits for the agent's control connection to reach the server.
func dialWhenUp(t *testing.T, from *machine, device string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := from.DialRelay(context.Background(), device)
		if err == nil {
			return c
		}
		if !errors.Is(err, ErrNotInRelay) || time.Now().After(deadline) {
			t.Fatalf("dial through the relay: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRelay(t *testing.T) {
	en := newEnv(t)
	target, caller := en.machine("target"), en.machine("caller") // both dev@example.com
	stranger := en.user("eve@example.com", "eve")
	addr := fakeSSHD(t)
	stop := startAgent(t, target, func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	})

	// One connection: the banner comes from the machine's sshd, bytes go both ways.
	c := dialWhenUp(t, caller, target.Cfg.DeviceID)
	banner := make([]byte, len("SSH-2.0-fake\r\n"))
	if _, err := io.ReadFull(c, banner); err != nil || string(banner) != "SSH-2.0-fake\r\n" {
		t.Fatalf("banner %q %v", banner, err)
	}
	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB, several websocket messages
	go func() { c.Write(big) }()
	got := make([]byte, len(big))
	if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("echo of %d bytes: %v", len(big), err)
	}
	c.Close()

	// Several at once, each its own stream.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := dialWhenUp(t, caller, target.Cfg.DeviceID)
			defer c.Close()
			io.ReadFull(c, make([]byte, len("SSH-2.0-fake\r\n")))
			msg := []byte(strings.Repeat("x", 1000+i))
			c.Write(msg)
			back := make([]byte, len(msg))
			if _, err := io.ReadFull(c, back); err != nil || !bytes.Equal(back, msg) {
				t.Errorf("stream %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// Another account cannot reach the machine, and a machine that is not
	// in the relay looks the same.
	if _, err := stranger.DialRelay(context.Background(), target.Cfg.DeviceID); !errors.Is(err, ErrNotInRelay) {
		t.Fatalf("another account: %v", err)
	}
	if _, err := caller.DialRelay(context.Background(), "0123456789abcdef"); !errors.Is(err, ErrNotInRelay) {
		t.Fatalf("unknown machine: %v", err)
	}

	// The agent leaves: the server notices and the machine is gone.
	stop()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := caller.DialRelay(context.Background(), target.Cfg.DeviceID)
		if errors.Is(err, ErrNotInRelay) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server still relays to a stopped agent: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A machine whose sshd is down says so to the one that asks.
func TestRelayBackendDown(t *testing.T) {
	en := newEnv(t)
	target, caller := en.machine("target"), en.machine("caller")
	startAgent(t, target, func(context.Context) (net.Conn, error) {
		return nil, errors.New("sshd is not running on that machine (hearth ssh status)")
	})
	c := dialWhenUp(t, caller, target.Cfg.DeviceID)
	defer c.Close()
	_, err := io.ReadAll(c)
	if err == nil || !strings.Contains(RelayReason(err).Error(), "sshd is not running") {
		t.Fatalf("reason: %v", err)
	}
}

func TestRelayOn(t *testing.T) {
	en := newEnv(t)
	m := en.machine("m")
	on := RelayOn(m.Home)
	if on != SSHOn(m.Home) {
		t.Fatalf("by default the relay follows [ssh] enabled: %v vs %v", on, SSHOn(m.Home))
	}
}
