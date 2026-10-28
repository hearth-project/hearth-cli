// Package paths expands the portable path forms used in synced settings:
// "~/x", "${HOME}/x" and "${WORKSPACE}/x". The workspace is the user's own
// working directory (several people may share one Unix account, so $HOME is
// not necessarily personal); the client exports it as $HEARTH_WORKSPACE.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// Workspace returns the current user's workspace ("" if none configured).
func Workspace() string { return os.Getenv("HEARTH_WORKSPACE") }

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// Expand resolves a leading ~/, ${HOME} or ${WORKSPACE}. A ${WORKSPACE}
// path without a configured workspace falls back to the home directory.
func Expand(p string) string {
	switch {
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home(), p[2:])
	case p == "${HOME}" || strings.HasPrefix(p, "${HOME}/"):
		return filepath.Join(home(), strings.TrimPrefix(p, "${HOME}"))
	case p == "${WORKSPACE}" || strings.HasPrefix(p, "${WORKSPACE}/"):
		ws := Workspace()
		if ws == "" {
			ws = home()
		}
		return filepath.Join(ws, strings.TrimPrefix(p, "${WORKSPACE}"))
	}
	return p
}

// ExpandWords expands every space-separated word (for command-like values).
func ExpandWords(v string) string {
	words := strings.Split(v, " ")
	for i, w := range words {
		words[i] = Expand(w)
	}
	return strings.Join(words, " ")
}

// Portable reports whether p is absolute or uses one of the prefixes.
func Portable(p string) bool {
	if strings.Contains(p, "..") {
		return false
	}
	return filepath.IsAbs(p) || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "${HOME}/") ||
		strings.HasPrefix(p, "${WORKSPACE}/") || p == "${WORKSPACE}" || p == "${HOME}"
}

// Contract rewrites an absolute path into the most portable form
// (${WORKSPACE}/..., then ~/...).
func Contract(p string) string {
	if ws := Workspace(); ws != "" {
		if p == ws {
			return "${WORKSPACE}"
		}
		if rest, ok := strings.CutPrefix(p, ws+string(filepath.Separator)); ok {
			return "${WORKSPACE}/" + rest
		}
	}
	if h := home(); h != "" {
		if rest, ok := strings.CutPrefix(p, h+string(filepath.Separator)); ok {
			return "~/" + rest
		}
	}
	return p
}
