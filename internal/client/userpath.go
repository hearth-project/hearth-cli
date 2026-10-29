package client

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The daemon often runs with the PATH of whatever started it (an installer,
// an SSH command, an old shell) and so misses tools the user installed
// afterwards in their own directories (~/.local/bin, nvm, npm's prefix).
// EnsureUserPath appends those directories to this process's PATH, after
// the existing entries so their order still wins, so the environment check
// and the commands Hearth runs see what the user's shells see.

var userPathOnce sync.Mutex

// userBinDirs are the per-user install directories that exist here.
func userBinDirs() []string {
	h, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dirs := []string{
		filepath.Join(h, ".local", "bin"), filepath.Join(h, "bin"), filepath.Join(h, ".npm-global", "bin"),
		filepath.Join(h, ".cargo", "bin"), filepath.Join(h, ".bun", "bin"), filepath.Join(h, ".claude", "local"),
		filepath.Join(h, ".volta", "bin"), filepath.Join(h, "Library", "pnpm"), filepath.Join(h, ".local", "share", "pnpm"),
		filepath.Join(h, ".yarn", "bin"), filepath.Join(h, ".codex-mixin", "bin"),
		"/usr/local/bin", "/opt/homebrew/bin",
	}
	// nvm and Hearth's own Node installs, newest version first.
	for _, pat := range []string{filepath.Join(h, ".nvm", "versions", "node", "*", "bin"), filepath.Join(h, ".hearth", "opt", "node-*", "bin")} {
		m, _ := filepath.Glob(pat)
		sort.Sort(sort.Reverse(sort.StringSlice(m)))
		dirs = append(dirs, m...)
	}
	var out []string
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

var loginPath struct {
	done bool
	dirs []string
}

// loginShellPath is the PATH of the user's login shell (once per process):
// tools live wherever their rc files point (a Mac's ~/.zprofile adds
// Homebrew, nvm, a tool's own bin), which a daemon started elsewhere never
// sees. Bounded by a timeout; rc files that start something interactive
// are cut off with their whole process group.
func loginShellPath() []string {
	if loginPath.done {
		return loginPath.dirs
	}
	loginPath.done = true
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
		if runtime.GOOS == "darwin" {
			sh = "/bin/zsh"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	const mark = "__HEARTH_LOGIN_PATH__="
	cmd := exec.CommandContext(ctx, sh, "-lic", `printf '\n%s%s\n' "`+mark+`" "$PATH"`)
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	out, _ := cmd.Output()
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), mark); ok {
			for _, d := range filepath.SplitList(v) {
				if filepath.IsAbs(d) {
					loginPath.dirs = append(loginPath.dirs, filepath.Clean(d))
				}
			}
		}
	}
	return loginPath.dirs
}

// EnsureUserPath adds the user's install directories missing from PATH:
// the login shell's PATH, then the usual per-user directories.
func EnsureUserPath() {
	userPathOnce.Lock()
	defer userPathOnce.Unlock()
	cur := filepath.SplitList(os.Getenv("PATH"))
	have := map[string]bool{}
	for _, d := range cur {
		have[filepath.Clean(d)] = true
	}
	added := false
	extra := userBinDirs()
	if os.Getenv("HEARTH_NO_LOGIN_PATH") == "" {
		extra = append(loginShellPath(), extra...)
	}
	for _, d := range extra {
		if !have[d] {
			cur = append(cur, d)
			have[d] = true
			added = true
		}
	}
	if added {
		os.Setenv("PATH", strings.Join(cur, string(os.PathListSeparator)))
	}
}
