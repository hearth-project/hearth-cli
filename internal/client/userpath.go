package client

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

// EnsureUserPath adds the user's install directories missing from PATH.
func EnsureUserPath() {
	userPathOnce.Lock()
	defer userPathOnce.Unlock()
	cur := filepath.SplitList(os.Getenv("PATH"))
	have := map[string]bool{}
	for _, d := range cur {
		have[filepath.Clean(d)] = true
	}
	added := false
	for _, d := range userBinDirs() {
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
