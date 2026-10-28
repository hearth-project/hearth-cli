package client

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/backup"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// BackupConfigPath is the user's upload selection, kept in the synced
// library so every machine follows the same choices.
func BackupConfigPath(home string) string {
	return filepath.Join(LibraryDir(home), backup.FileName)
}

// BackupText renders config.toml's [backup] section in the .gitignore-like
// format the parser and `hearth backup show` use. The old backup.conf is
// read when config.toml has no section (and migrated into it on save).
func BackupText(home string) string {
	if c, err := UserConfig(home); err == nil && backupSet(&c) {
		var b strings.Builder
		var cats []string
		for k := range c.Backup.Categories {
			cats = append(cats, k)
		}
		sort.Strings(cats)
		for _, k := range cats {
			if c.Backup.Categories[k] {
				b.WriteString("@" + k + "\n")
			} else {
				b.WriteString("!@" + k + "\n")
			}
		}
		for _, p := range c.Backup.Include {
			b.WriteString(p + "\n")
		}
		for _, p := range c.Backup.Exclude {
			if !strings.HasPrefix(p, "@") {
				b.WriteString("!" + p + "\n")
			}
		}
		return b.String()
	}
	b, _ := os.ReadFile(BackupConfigPath(home))
	return string(b)
}

// BackupSelection returns the parsed selection for this machine's home and
// workspace (the default when the user has none yet).
func BackupSelection(home string) *backup.Config {
	h, _ := os.UserHomeDir()
	if c, err := UserConfig(home); err == nil && backupSet(&c) {
		return backup.Parse(BackupText(home), h, paths.Workspace())
	}
	return backup.Cached(BackupConfigPath(home), h, paths.Workspace())
}

// EnsureBackupConfig makes sure the user has a selection to edit: it puts
// an empty [backup] section in config.toml (all categories on). Returns
// true when it was created.
func EnsureBackupConfig(home string) (bool, error) {
	c, err := UserConfig(home)
	if err != nil {
		return false, err
	}
	if backupSet(&c) {
		return false, nil
	}
	c.Backup = &library.BackupConfig{Categories: defaultCategories()}
	return true, SaveUserConfig(home, c)
}

// SetBackupCategory turns one category on or off in config.toml.
func SetBackupCategory(home, cat string, on bool) error {
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	if c.Backup == nil {
		c.Backup = &library.BackupConfig{}
	}
	key := strings.TrimPrefix(strings.ToLower(cat), "@")
	if c.Backup.Categories == nil {
		c.Backup.Categories = defaultCategories()
	}
	c.Backup.Categories[key] = on
	out := c.Backup.Exclude[:0]
	for _, x := range c.Backup.Exclude {
		if !strings.EqualFold(strings.TrimPrefix(x, "@"), key) {
			out = append(out, x)
		}
	}
	c.Backup.Exclude = out
	return SaveUserConfig(home, c)
}

// AddBackupRule appends one include (exclude=false) or exclude rule and
// reports whether it was new.
func AddBackupRule(home, pattern string, exclude bool) (bool, error) {
	c, err := UserConfig(home)
	if err != nil {
		return false, err
	}
	if c.Backup == nil {
		c.Backup = &library.BackupConfig{}
	}
	if strings.HasPrefix(pattern, "@") {
		if c.Backup.Categories == nil {
			c.Backup.Categories = defaultCategories()
		}
		key := strings.TrimPrefix(pattern, "@")
		if cur, ok := c.Backup.Categories[key]; ok && cur == !exclude {
			return false, nil
		}
		c.Backup.Categories[key] = !exclude
		return true, SaveUserConfig(home, c)
	}
	list := &c.Backup.Include
	if exclude {
		list = &c.Backup.Exclude
	}
	for _, x := range *list {
		if x == pattern {
			return false, nil
		}
	}
	*list = append(*list, pattern)
	return true, SaveUserConfig(home, c)
}

// EnsureBackupConfig (engine form).
func (e *Engine) EnsureBackupConfig() (bool, error) {
	return EnsureBackupConfig(e.Home)
}

// selectedAdapter syncs the extra files the selection lists.
func selectedAdapter(home string) adapter.Adapter {
	return adapter.SelectedFiles(func() (string, string, []string, func(string) bool) {
		h, _ := os.UserHomeDir()
		cfg := BackupSelection(home)
		return h, paths.Workspace(), cfg.Roots(), cfg.Included
	})
}

// backupKeys are the selection keys of a local file, most specific first
// (nil = path rules decide, as for user-selected files). Agent data is
// keyed by agent and kind: "codex.sessions", "codex", "sessions".
func backupKeys(f adapter.SessionFile) []string {
	switch f.Agent {
	case "files":
		return nil
	case "workspace":
		return []string{backup.Workspace}
	}
	kind := backup.Sessions
	if f.Kind == adapter.KindMemory {
		kind = backup.Memory
	}
	return []string{f.Agent + "." + kind, f.Agent, kind}
}

// backupCategory is the display name of a file's most specific key.
func backupCategory(f adapter.SessionFile) string {
	if k := backupKeys(f); len(k) > 0 {
		return k[0]
	}
	return ""
}

// selected reports whether the user's selection allows uploading f.
func (e *Engine) selected(f adapter.SessionFile) bool {
	cfg := BackupSelection(e.Home)
	if cfg.Excluded(f.AbsPath) {
		return false
	}
	if keys := backupKeys(f); keys != nil {
		return cfg.Selected(keys...)
	}
	return cfg.Included(f.AbsPath)
}

// DefaultBackupConfig lists every registered agent and its kinds.
func DefaultBackupConfig() string {
	var agents []backup.Agent
	for _, in := range Integrations() {
		c := in.Capabilities()
		a := backup.Agent{Name: in.Name()}
		if c.Sessions {
			a.Kinds = append(a.Kinds, backup.Sessions)
		}
		if c.Memory {
			a.Kinds = append(a.Kinds, backup.Memory)
		}
		agents = append(agents, a)
	}
	return backup.DefaultFor(agents)
}

// BackupCategories lists the selection keys Hearth understands: per agent
// (from the agent integrations) and the general ones.
func BackupCategories() []string {
	out := []string{backup.Workspace, backup.Credentials, backup.Sessions, backup.Memory}
	for _, in := range Integrations() {
		c := in.Capabilities()
		out = append(out, in.Name())
		if c.Sessions {
			out = append(out, in.Name()+"."+backup.Sessions)
		}
		if c.Memory {
			out = append(out, in.Name()+"."+backup.Memory)
		}
	}
	return out
}

// BackupRow is one local file and whether the selection uploads it.
type BackupRow struct {
	Category string // sessions, memory, workspace, files
	Path     string // ~ / ${WORKSPACE} form
	Size     int64
	Selected bool
}

// BackupPlan lists this user's local files with their selection status.
func (e *Engine) BackupPlan() ([]BackupRow, error) {
	roots := ownedRoots(e.Home)
	var out []BackupRow
	for _, a := range e.Adapters {
		files, err := a.Sessions()
		if err != nil {
			return out, err
		}
		for _, f := range files {
			if !e.owns(f, roots) {
				continue
			}
			cat := backupCategory(f)
			if cat == "" {
				cat = "files"
			}
			var size int64
			if fi, err := os.Stat(f.AbsPath); err == nil {
				size = fi.Size()
			}
			out = append(out, BackupRow{Category: cat, Path: paths.Contract(f.AbsPath), Size: size, Selected: e.selected(f)})
		}
	}
	return out, nil
}

// CredentialCount is the number of stored credentials (key/login files and
// tokens); an account with any already chose to back credentials up.
func (e *Engine) CredentialCount() (int, error) {
	all, err := e.Secrets()
	if err != nil {
		return 0, err
	}
	return len(all), nil
}
