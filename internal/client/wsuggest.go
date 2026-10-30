package client

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hearth-project/hearth-cli/internal/api"
	mach "github.com/hearth-project/hearth-cli/internal/machine"
)

// WorkspaceSuggestion is the working directory proposed for a new machine:
// the one the user's machines of the same kind use (same GPU model, else
// the same GPU family such as B / GB series, else the same OS and CPU), or
// none (the user's home) when no machine is like this one.
type WorkspaceSuggestion struct {
	Path    string // "" = the user's home (no separate workspace)
	Machine string // the machine it comes from
	Kind    string // what makes it alike: "gpu cf-ng-bzz2-o", "gb family", "linux/x86_64"
}

// SuggestWorkspace picks the workspace of the most alike, most recently
// seen active device other than self.
func SuggestWorkspace(ds []api.Device, self string, f mach.Facts) WorkspaceSuggestion {
	best, bestScore, bestSeen := WorkspaceSuggestion{}, 0, int64(0)
	for _, d := range ds {
		if d.ID == self || (d.Status != "" && d.Status != "active") {
			continue
		}
		ws, _ := d.Machine["workspace"].(string)
		facts, _ := d.Machine["facts"].(map[string]any)
		if ws == "" || facts == nil {
			continue
		}
		s := func(k string) string { v, _ := facts[k].(string); return v }
		score, kind := 0, ""
		switch {
		case f.GPU != "" && s("gpu") == f.GPU:
			score, kind = 3, "GPU "+f.GPU
		case f.GPU != "" && gpuFamily(s("gpu")) != "" && gpuFamily(s("gpu")) == gpuFamily(f.GPU):
			score, kind = 2, gpuFamily(f.GPU)+" series GPU"
		case f.GPU == "" && s("gpu") == "" && s("os") == f.OS && s("arch") == f.Arch:
			score, kind = 1, f.OS+"/"+f.Arch
		}
		if score > bestScore || (score == bestScore && score > 0 && d.LastSeen > bestSeen) {
			best, bestScore, bestSeen = WorkspaceSuggestion{Path: ws, Machine: d.Name, Kind: kind}, score, d.LastSeen
		}
	}
	return best
}

// MoveHome moves a Hearth home (login, keys, library, state) into
// <ws>/.hearth and marks ws as a workspace; across disks it copies and
// then removes the old one. The daemon of from must not be running.
func MoveHome(from, ws string) (string, error) {
	abs, err := filepath.Abs(ws)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", err
	}
	target := WorkspaceHome(abs)
	if filepath.Clean(target) == filepath.Clean(from) {
		return target, nil
	}
	if name := OwnState(target); name != "" {
		return "", fmt.Errorf("%s already holds hearth state (%s); move it away first", target, name)
	}
	_ = os.RemoveAll(target)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(from, target); err != nil {
		var le *os.LinkError
		if !errors.As(err, &le) || !errors.Is(le.Err, syscall.EXDEV) {
			return "", err
		}
		if err := copyTreeX(from, target); err != nil {
			_ = os.RemoveAll(target)
			return "", fmt.Errorf("copy %s to %s: %w", from, target, err)
		}
		if err := os.RemoveAll(from); err != nil {
			return "", err
		}
	}
	if _, err := InitWorkspace(abs); err != nil {
		return "", err
	}
	return target, nil
}

// copyTreeX copies a directory with modes and symlinks (a whole home).
func copyTreeX(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(l, to)
		case fi.IsDir():
			return os.MkdirAll(to, fi.Mode().Perm()|0o700)
		case !fi.Mode().IsRegular():
			return nil // sockets, lock files of a stopped daemon
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// ExpandUserDir resolves ~ and $HOME in a directory typed by the user.
func ExpandUserDir(p string) string {
	p = strings.TrimSpace(p)
	h, _ := os.UserHomeDir()
	for _, pre := range []string{"${HOME}", "$HOME", "~"} {
		if rest, ok := strings.CutPrefix(p, pre); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
			p = h + rest
			break
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
