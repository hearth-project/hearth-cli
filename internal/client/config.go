// Package client implements the hearth CLI side: login, the encrypted
// session sync engine and restore.
package client

import (
	"github.com/hearth-project/hearth-cli/internal/paths"

	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// Home returns the client state directory: $HEARTH_HOME, else the
// .hearth directory of the workspace containing the current directory
// (found like .git, so several people sharing one Unix account each get
// their own state), else ~/.hearth.
func Home() string {
	if h := os.Getenv("HEARTH_HOME"); h != "" {
		return h
	}
	if ws := FindWorkspace("."); ws != "" {
		return filepath.Join(ws, ".hearth")
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ".hearth"
	}
	return filepath.Join(h, ".hearth")
}

// workspaceMarker marks <workspace>/.hearth as a workspace's state dir.
const workspaceMarker = "workspace.json"

type workspaceFile struct {
	Path string `json:"path"`
}

// FindWorkspace walks up from dir to the nearest initialised workspace.
func FindWorkspace(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(d, ".hearth", workspaceMarker)); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// InitWorkspace makes dir a workspace and returns its hearth home.
func InitWorkspace(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	home := filepath.Join(abs, ".hearth")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	// The installer put the server CA into ~/.hearth before this workspace
	// existed: bring it along.
	if err := adoptCA(home); err != nil {
		return "", err
	}
	return home, saveJSON(filepath.Join(home, workspaceMarker), workspaceFile{Path: abs})
}

// InitOnlyFile reports whether a file in a home is one InitWorkspace writes
// (so a home holding only those has no state of its own yet).
func InitOnlyFile(name string) bool { return name == workspaceMarker || name == "ca.pem" }

// OwnState returns the first entry of home that holds state of its own
// ("" when there is none): anything but init's files and empty
// directories, which commands such as `hearth mcp` create before a login.
func OwnState(home string) string {
	entries, _ := os.ReadDir(home)
	for _, en := range entries {
		if InitOnlyFile(en.Name()) {
			continue
		}
		if en.IsDir() && emptyTree(filepath.Join(home, en.Name())) {
			continue
		}
		return en.Name()
	}
	return ""
}

func emptyTree(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, en := range entries {
		if !en.IsDir() || !emptyTree(filepath.Join(dir, en.Name())) {
			return false
		}
	}
	return true
}

// MovableLogin returns the login of ~/.hearth when home is a workspace
// that is not logged in yet and holds no state of its own, so that
// `hearth init --move-login` can bring that login here. Whether that login
// is this person's is for the caller to judge: on a shared Unix account it
// may be someone else's.
func MovableLogin(home string) (*Config, bool) {
	def := DefaultHome()
	if filepath.Clean(def) == filepath.Clean(home) || WorkspaceOf(home) == "" {
		return nil, false
	}
	if _, err := LoadConfig(home); err == nil {
		return nil, false
	}
	cfg, err := LoadConfig(def)
	if err != nil || OwnState(home) != "" {
		return nil, false
	}
	return cfg, true
}

// WorkspaceOf returns the workspace a home belongs to ("" for ~/.hearth).
func WorkspaceOf(home string) string {
	var w workspaceFile
	if loadJSON(filepath.Join(home, workspaceMarker), &w) == nil {
		return w.Path
	}
	return ""
}

// Activate exports the environment every hearth process (and the
// daemon / MCP children it starts) should see: HEARTH_HOME,
// HEARTH_WORKSPACE and the configured agent homes (CODEX_HOME, ...),
// without overriding values the user already set.
func Activate() string {
	home := Home()
	os.Setenv("HEARTH_HOME", home)
	if ws := WorkspaceOf(home); ws != "" && os.Getenv("HEARTH_WORKSPACE") == "" {
		os.Setenv("HEARTH_WORKSPACE", ws)
	}
	// Otherwise the working directory of this kind of machine, if a
	// profile (library/machines.toml) defines one.
	if os.Getenv("HEARTH_WORKSPACE") == "" {
		if ws := profileWorkspace(home); ws != "" {
			os.Setenv("HEARTH_WORKSPACE", ws)
			os.Setenv("HEARTH_WORKSPACE_FROM_PROFILE", "1")
		}
	}
	for agent, p := range AgentHomes(home) {
		if v, ok := agentHomeEnv[agent]; ok && os.Getenv(v) == "" {
			os.Setenv(v, paths.Expand(p))
		}
	}
	return home
}

// Config is the per-machine login state.
type Config struct {
	Server     string `json:"server"`
	Email      string `json:"email"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Token      string `json:"token"`
	ExpiresAt  int64  `json:"expires_at"`
}

// ErrNotLoggedIn is returned when no config or vault key exists.
var ErrNotLoggedIn = errors.New("not logged in on this machine: run `hearth setup` (or ask your agent: \"用 hearth\")")

// writeFileAtomic writes data with mode perm via temp file + rename.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func jsonIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

func saveJSON(path string, v any) error {
	b, err := jsonIndent(v)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b, 0o600)
}

// LoadConfig reads config.json.
func LoadConfig(home string) (*Config, error) {
	var c Config
	if err := loadJSON(filepath.Join(home, "config.json"), &c); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotLoggedIn
		}
		return nil, err
	}
	return &c, nil
}

// SaveConfig writes config.json with 0600 permissions.
func SaveConfig(home string, c *Config) error { return saveJSON(filepath.Join(home, "config.json"), c) }

// The decrypted vault key is cached locally (0600) so the daemon can run
// without the password. Anyone with root on this machine can read it; see
// the threat model in the design doc (section 5.3).
func saveVaultKey(home string, key []byte) error {
	return writeFileAtomic(filepath.Join(home, "vault.key"), []byte(base64.StdEncoding.EncodeToString(key)), 0o600)
}

// LoadVault opens the cached vault key.
func LoadVault(home string) (*crypto.Vault, error) {
	b, err := os.ReadFile(filepath.Join(home, "vault.key"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotLoggedIn
		}
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil {
		return nil, fmt.Errorf("corrupt vault.key: %w", err)
	}
	return crypto.OpenVault(key)
}

// Logout removes local credentials (synced data on the server is kept).
func Logout(home string) error {
	for _, f := range []string{"config.json", "vault.key"} {
		if err := os.Remove(filepath.Join(home, f)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
