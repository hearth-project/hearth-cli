package client

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// setupHintsDir is local to this Unix account, host, server and Hearth user.
// Each file contains only a workspace path, never a login or vault key.
func setupHintsDir(server, email string) string {
	dir, err := os.UserCacheDir()
	if err != nil || server == "" || email == "" {
		return ""
	}
	key := strings.Join([]string{hostKey(), strings.TrimRight(server, "/"), strings.ToLower(email)}, "\x00")
	return filepath.Join(dir, "hearth", "setup-workspaces", fmt.Sprintf("%x", sha256.Sum256([]byte(key))))
}

// RememberSetupWorkspace lets the next install find this login even if SSH
// is disabled and the shell doesn't source an identity block.
func RememberSetupWorkspace(home string, cfg *Config) {
	ws := WorkspaceOf(home)
	if !filepath.IsAbs(ws) {
		return
	}
	dir := setupHintsDir(cfg.Server, cfg.Email)
	if dir == "" || os.MkdirAll(dir, 0o700) != nil {
		return
	}
	_ = writeFileAtomic(filepath.Join(dir, fmt.Sprintf("%x", sha256.Sum256([]byte(ws)))), []byte(ws), 0o600)
}

// WorkspaceLogin is a local candidate, not an authenticated identity.
type WorkspaceLogin struct {
	Home, Workspace string
	Config          *Config
}

// SetupWorkspaces finds candidate logins outside the current directory,
// without walking a shared filesystem or evaluating shell configuration.
// Paths are only hints: verify the workspace, owner and account locally;
// the caller must still prove the existing login before using it.
func SetupWorkspaces(server, email string) []WorkspaceLogin {
	candidates := map[string]bool{}
	if dir := setupHintsDir(server, email); dir != "" {
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			if !f.Type().IsRegular() || len(f.Name()) != 64 {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(dir, f.Name())); err == nil && filepath.IsAbs(string(b)) {
				candidates[WorkspaceHome(string(b))] = true
			}
		}
	}
	// Pre-existing clients leave this exact marker in the shell rc files.
	// Only read the generated marker; do not source the file or the script.
	h, _ := os.UserHomeDir()
	for _, rc := range []string{".bashrc", ".zshrc"} {
		b, _ := os.ReadFile(filepath.Join(h, rc))
		for line := range strings.SplitSeq(string(b), "\n") {
			if p, ok := strings.CutPrefix(line, "# <<< hearth env "); ok {
				if home, ok := strings.CutSuffix(p, "/shell/identity.sh <<<"); ok && filepath.IsAbs(home) {
					candidates[filepath.Clean(home)] = true
				}
			}
		}
	}
	var matches []WorkspaceLogin
	for home := range candidates {
		ws := WorkspaceOf(home)
		if !filepath.IsAbs(ws) || WorkspaceHome(ws) != home || home == DefaultHome() {
			continue
		}
		cfg, err := LoadConfig(home)
		if err != nil || cfg.DeviceID == "" || !strings.EqualFold(cfg.Email, email) || strings.TrimRight(cfg.Server, "/") != strings.TrimRight(server, "/") {
			continue
		}
		// Per-host directory or explicit local owner. For a legacy base with
		// no owner marker, require an exact hostname, not a short-name match.
		marker, err := os.ReadFile(filepath.Join(home, hostMarker))
		if err == nil {
			if strings.TrimSpace(string(marker)) != hostKey() {
				continue
			}
		} else if !os.IsNotExist(err) || (home == filepath.Join(ws, ".hearth") && !strings.EqualFold(cfg.DeviceName, hostname())) {
			continue
		}
		matches = append(matches, WorkspaceLogin{Home: home, Workspace: ws, Config: cfg})
	}
	slices.SortFunc(matches, func(a, b WorkspaceLogin) int { return strings.Compare(a.Home, b.Home) })
	return matches
}
