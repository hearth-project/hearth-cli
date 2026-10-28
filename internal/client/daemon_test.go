package client

import (
	"errors"
	"os"
	"testing"
)

func TestDaemonSingleInstanceLock(t *testing.T) {
	home := t.TempDir()
	if pid := DaemonPID(home); pid != 0 {
		t.Fatalf("pid %d before start", pid)
	}
	f, err := LockDaemon(home)
	if err != nil {
		t.Fatal(err)
	}
	if pid := DaemonPID(home); pid != os.Getpid() {
		t.Fatalf("pid %d, want %d", pid, os.Getpid())
	}
	if _, err := LockDaemon(home); !errors.Is(err, ErrDaemonRunning) {
		t.Fatalf("second lock: %v", err)
	}
	f.Close()
	if pid := DaemonPID(home); pid != 0 {
		t.Fatalf("pid %d after release", pid)
	}
}
