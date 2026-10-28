package client

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Daemon process management. A single daemon per HEARTH_HOME is
// enforced with an flock on daemon.lock; the lock, not the pid file, is the
// source of truth for "is it running".

func lockPath(home string) string { return filepath.Join(home, "daemon.lock") }
func pidPath(home string) string  { return filepath.Join(home, "daemon.pid") }

// DaemonLog is where a background daemon writes its log.
func DaemonLog(home string) string { return filepath.Join(home, "daemon.log") }

// ErrDaemonRunning is returned when another daemon holds the lock.
var ErrDaemonRunning = errors.New("an hearth daemon is already running for this HEARTH_HOME")

// LockDaemon takes the single-instance lock and records our pid. Keep the
// returned file open for the daemon's lifetime.
func LockDaemon(home string) (*os.File, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath(home), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrDaemonRunning
		}
		return nil, err
	}
	if err := writeFileAtomic(pidPath(home), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// DaemonPID returns the pid of the running daemon, or 0 if none.
func DaemonPID(home string) int {
	f, err := os.OpenFile(lockPath(home), os.O_RDWR, 0o600)
	if err != nil {
		return 0
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return 0 // nobody holds it
	}
	b, err := os.ReadFile(pidPath(home))
	if err != nil {
		return -1 // running, pid unknown
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

// StartDaemon launches `exe daemon` detached from the terminal, logging to
// DaemonLog(home), and waits until it holds the lock.
func StartDaemon(home, exe string, extraArgs ...string) (int, error) {
	if pid := DaemonPID(home); pid != 0 {
		return pid, ErrDaemonRunning
	}
	logf, err := os.OpenFile(DaemonLog(home), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer logf.Close()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, err
	}
	defer devnull.Close()
	cmd := exec.Command(exe, append([]string{"daemon"}, extraArgs...)...)
	cmd.Env = append(os.Environ(), "HEARTH_HOME="+home)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	for i := 0; i < 50; i++ {
		if DaemonPID(home) != 0 {
			return pid, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return pid, fmt.Errorf("daemon did not start; see %s", DaemonLog(home))
}

// StopDaemon sends SIGTERM (the daemon flushes and releases leases) and
// waits up to timeout for it to exit.
func StopDaemon(home string, timeout time.Duration) error {
	pid := DaemonPID(home)
	switch {
	case pid == 0:
		return errors.New("daemon is not running")
	case pid < 0:
		return errors.New("daemon is running but its pid file is missing")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if DaemonPID(home) == 0 {
			return nil
		}
	}
	return fmt.Errorf("daemon (pid %d) did not exit within %s", pid, timeout)
}
