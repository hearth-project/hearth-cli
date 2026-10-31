package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
)

// Own agent homes on shared accounts. When several people share one Unix
// account, ~/.codex is the config every Codex session falls back to. If
// that config is this person's (their codex-mixin gateway and client key),
// everybody whose shell does not set CODEX_HOME uses their gateway login.
// IsolateAgentHome moves this person's agent home into their workspace:
// their config and conversations are copied, codex-mixin connects the new
// home (a client key of its own), and the shared default is handed back
// to codex-mixin's uninstall (or stripped of the gateway), so it carries
// no account.
//
// The choice is per machine (<home>/agent-homes.json): another machine of
// the same person may be theirs alone and keep ~/.codex.

func localHomesPath(home string) string { return filepath.Join(home, "agent-homes.json") }

// LocalAgentHomes are the agent homes chosen on this machine only.
func LocalAgentHomes(home string) map[string]string {
	m := map[string]string{}
	loadJSON(localHomesPath(home), &m)
	return m
}

func setLocalAgentHome(home, agent, dir string) error {
	m := LocalAgentHomes(home)
	m[agent] = dir
	b, err := jsonIndent(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(localHomesPath(home), b, 0o600)
}

// AgentHomeOf is the config dir an agent uses for this person here.
func AgentHomeOf(home, agent string) string {
	if v, ok := agentHomeEnv[agent]; ok {
		if d := agentHomeExports(home)[v]; d != "" {
			return d
		}
	}
	return defaultAgentHome(agent)
}

// OwnAgentHome is where isolate puts an agent's home: inside this person's
// Hearth home, so it is per machine even when the workspace is on a disk
// several machines mount, and it is not the workspace's own project
// config dir (<workspace>/.codex holds project skills and agents that
// Codex loads as a project layer).
func OwnAgentHome(home, agent string) string {
	return filepath.Join(home, "agent-homes", agent)
}

func defaultAgentHome(agent string) string {
	h, _ := os.UserHomeDir()
	switch agent {
	case "codex":
		return filepath.Join(h, ".codex")
	case "claude":
		return filepath.Join(h, ".claude")
	}
	return ""
}

// SharedDefaultHome reports whether, in a workspace (a shared account), an
// agent still uses the account-wide default dir.
func SharedDefaultHome(home, agent string) bool {
	if WorkspaceOf(home) == "" {
		return false
	}
	d := defaultAgentHome(agent)
	return d != "" && filepath.Clean(AgentHomeOf(home, agent)) == filepath.Clean(d)
}

// IsolateResult says what IsolateAgentHome did.
type IsolateResult struct {
	Agent, From, To string
	Already         bool
	Copied          []string // top-level entries of the config dir
	Sessions        int      // conversations of this person copied
	Indexed         int      // Codex threads registered in the new home
	Gateway         string   // codex-mixin connect result
	Cleaned         string   // what happened to the shared default
	Backup          string   // copy of the shared default's config before cleaning
	Notes           []string
}

// Per-machine state, logs and caches an agent rebuilds; conversations are
// copied separately (only this person's).
var isolateSkip = map[string][]string{
	"codex": {"sessions", "archived_sessions", "session_index.jsonl", "history.jsonl", "log", "logs", "shell_snapshots",
		"tmp", ".tmp", "thread-writer-locks", "app-server-control", "app-server-daemon", "ipc", "attachments", "visualizations",
		"cache", "models_cache.json", "installation_id", "packages", ".sandbox_migration"},
	"claude": {"projects", "todos", "shell-snapshots", "statsig", "logs", "ide", "session-env", "file-history", "history.jsonl",
		"debug", "cache", "plans"},
}

func skipEntry(agent, name string, src string) bool {
	for _, s := range isolateSkip[agent] {
		if name == s {
			return true
		}
	}
	// codex-mixin's .absent / .backup markers travel with the config: they
	// say what to restore on uninstall-codex in the new home too.
	for _, suf := range []string{".sqlite", ".sqlite-shm", ".sqlite-wal", ".lock"} {
		if strings.HasSuffix(name, suf) {
			return true
		}
	}
	// codex-mixin wrote auth.json (a placeholder): connect writes the new
	// one. Its markers stay behind with it: a restore point for an auth.json
	// that is not there makes connect refuse ("Codex auth changed").
	if agent == "codex" && fileExists(filepath.Join(src, "auth.json.codex-mixin.absent")) &&
		(name == "auth.json" || strings.HasPrefix(name, "auth.json.codex-mixin.")) {
		return true
	}
	return false
}

func fileExists(p string) bool { _, err := os.Lstat(p); return err == nil }

// mixinProvider reports whether a Codex config goes through codex-mixin.
func mixinProvider(dir string) (provider string, ok bool) {
	p, base := codexConfigRoute(dir)
	return p, strings.HasPrefix(p, "codex-mixin") || (p != "" && isLoopbackURL(base))
}

// IsolateAgentHome moves this person's agent home on a shared account to
// dir (default OwnAgentHome). logf reports progress.
func (e *Engine) IsolateAgentHome(agent, dir string, logf func(string, ...any)) (IsolateResult, error) {
	r := IsolateResult{Agent: agent}
	ws := WorkspaceOf(e.Home)
	if ws == "" {
		return r, errors.New("only for a workspace (hearth init <dir>): without one this account is assumed to be yours alone")
	}
	if _, ok := isolateSkip[agent]; !ok {
		return r, fmt.Errorf("supported agents: codex, claude")
	}
	if dir == "" {
		dir = OwnAgentHome(e.Home, agent)
	}
	dst, err := filepath.Abs(expandWorkspace(dir))
	if err != nil {
		return r, err
	}
	src := AgentHomeOf(e.Home, agent)
	r.From, r.To = src, dst
	if filepath.Clean(src) == filepath.Clean(dst) {
		r.Already = true
		return r, nil
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return r, err
	}
	provider, viaMixin := "", false
	if agent == "codex" {
		provider, viaMixin = mixinProvider(src)
	}

	// 1. Config, skills, rules, plugins ...: copied, never overwriting.
	ents, _ := os.ReadDir(src)
	for _, en := range ents {
		if skipEntry(agent, en.Name(), src) {
			continue
		}
		n, err := copyHome(filepath.Join(src, en.Name()), filepath.Join(dst, en.Name()), src, dst)
		if err != nil {
			return r, fmt.Errorf("copy %s: %w", en.Name(), err)
		}
		if n > 0 {
			r.Copied = append(r.Copied, en.Name())
		}
	}
	if agent == "claude" && filepath.Clean(src) == filepath.Clean(defaultAgentHome("claude")) {
		// Claude Code keeps ~/.claude.json next to the dir, $CLAUDE_CONFIG_DIR/.claude.json inside it.
		h, _ := os.UserHomeDir()
		if n, _ := copyHome(filepath.Join(h, ".claude.json"), filepath.Join(dst, ".claude.json"), src, dst); n > 0 {
			r.Copied = append(r.Copied, ".claude.json")
		}
	}
	logf("copied %d entries of %s to %s", len(r.Copied), src, dst)

	// 2. This person's conversations (the session scope decides whose).
	var from adapter.Adapter
	if agent == "codex" {
		from = adapter.CodexAt(src)
	} else {
		from = adapter.ClaudeAt(src)
	}
	files, _ := from.Sessions()
	roots := ownedRoots(e.Home)
	want := map[string]adapter.ThreadInfo{}
	var ids []string
	for _, f := range files {
		f.Agent = agent
		if !e.owns(f, roots) {
			continue
		}
		n, err := copyHome(f.AbsPath, filepath.Join(dst, filepath.FromSlash(f.RelPath)), "", "")
		if err != nil {
			return r, fmt.Errorf("copy %s: %w", f.RelPath, err)
		}
		if n == 0 {
			continue
		}
		r.Sessions++
		if id := adapter.CodexThreadID(f.RelPath); agent == "codex" && id != "" {
			ids = append(ids, id)
			if f.Thread != nil {
				want[id] = *f.Thread
			}
		}
	}
	logf("copied %d of your conversations", r.Sessions)

	// 3. This machine uses the new home from now on.
	if err := setLocalAgentHome(e.Home, agent, dst); err != nil {
		return r, err
	}
	os.Setenv(agentHomeEnv[agent], dst)
	if _, err := EnsureIdentityRC(e.Home, e.Cfg.Email); err != nil {
		r.Notes = append(r.Notes, "shell block: "+err.Error())
	}
	if MemoryHooksOn(e.Home) {
		if _, err := SyncMemoryHooks(e.Home, true); err != nil {
			r.Notes = append(r.Notes, "memory hooks: "+err.Error())
		}
	}

	// 4. codex-mixin: a client key of the new home's own.
	mixin, _ := LookTool("codex-mixin")
	if viaMixin {
		if mixin == "" {
			r.Gateway = "codex-mixin not found: the copied config keeps the old client key"
		} else {
			args := []string{"connect", "codex", "--no-tui"}
			if strings.HasSuffix(provider, "-custom") {
				args = append(args, "--custom-only")
			}
			out, err := runAgentTool(mixin, dst, args...)
			r.Gateway = "connected " + dst
			if err != nil {
				r.Gateway = "codex-mixin connect failed (" + err.Error() + "): " + lastLine(out) + "; the copied config keeps the old client key"
			}
		}
		logf("codex-mixin: %s", r.Gateway)
	}
	if agent == "codex" && len(ids) > 0 {
		if st, err := ApplyCodexThreads(dst, want, ids...); err != nil {
			r.Notes = append(r.Notes, "Codex thread list: "+truncHealth(err.Error(), 160)+" (Codex lists them once it reads the sessions)")
		} else {
			r.Indexed = st.Indexed
		}
	}

	// 5. The shared default no longer carries this person's account.
	if filepath.Clean(src) == filepath.Clean(defaultAgentHome(agent)) {
		r.Cleaned, r.Backup = e.cleanSharedDefault(agent, src, viaMixin, mixin)
		logf("%s: %s", src, r.Cleaned)
	}
	return r, nil
}

// cleanSharedDefault removes this person's gateway login and Hearth hooks
// from the account-wide default dir, after a backup into their own home.
func (e *Engine) cleanSharedDefault(agent, src string, viaMixin bool, mixin string) (string, string) {
	var done []string
	backup := filepath.Join(e.Home, "backup", agent+"-default-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(backup, 0o700); err != nil {
		return "not cleaned: " + err.Error(), ""
	}
	for _, n := range []string{"config.toml", "auth.json", "hooks.json", "settings.json"} {
		_, _ = copyHome(filepath.Join(src, n), filepath.Join(backup, n), "", "")
	}
	switch hooks := map[string]string{"codex": "hooks.json", "claude": "settings.json"}[agent]; {
	case hooks != "":
		if op, err := editHooks(filepath.Join(src, hooks), "", false); err == nil && op != "unchanged" && op != "" {
			done = append(done, "Hearth hooks removed")
		}
	}
	if agent == "codex" && viaMixin {
		cleaned := false
		if mixin != "" {
			if _, err := runAgentTool(mixin, src, "uninstall-codex", "--no-tui"); err == nil {
				cleaned = true
				done = append(done, "codex-mixin uninstall-codex restored the config it replaced")
			}
		}
		if !cleaned {
			if err := stripMixinConfig(filepath.Join(src, "config.toml")); err != nil {
				done = append(done, "gateway not removed: "+err.Error())
			} else {
				done = append(done, "codex-mixin provider and client key removed from config.toml")
			}
		}
	}
	if len(done) == 0 {
		_ = os.RemoveAll(backup)
		return "nothing of yours to remove", ""
	}
	return strings.Join(done, "; "), backup
}

// stripMixinConfig drops codex-mixin's provider selection, catalog and
// provider tables (with the client key) from a Codex config.toml.
func stripMixinConfig(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var out []string
	inMixin := false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			name := strings.Trim(t, "[] ")
			inMixin = strings.HasPrefix(name, "model_providers.codex-mixin") || strings.HasPrefix(name, `model_providers."codex-mixin`)
		}
		if inMixin {
			continue
		}
		key, val, _ := strings.Cut(t, "=")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if (key == "model_provider" && strings.Contains(val, "codex-mixin")) ||
			(key == "model_catalog_json" && strings.Contains(val, "mixin")) {
			continue
		}
		out = append(out, line)
	}
	return writeFileAtomic(path, []byte(strings.Join(out, "\n")), 0o600)
}

// runAgentTool runs an agent tool against an explicit agent home.
func runAgentTool(exe, codexHome string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return truncHealth(lines[len(lines)-1], 200)
}

// copyHome copies a file, symlink or directory without overwriting what
// dst already has, keeping modes and times. Text files that name the old
// dir (from) are rewritten to the new one (to) when both are set: Codex's
// config.toml holds absolute paths into its home. Returns files written.
func copyHome(src, dst, from, to string) (int, error) {
	n := 0
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == src {
				return fs.SkipAll
			}
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(out, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			if fileExists(out) {
				return nil
			}
			t, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if from != "" && strings.HasPrefix(t, from+string(filepath.Separator)) {
				t = to + strings.TrimPrefix(t, from)
			}
			n++
			return os.Symlink(t, out)
		case !info.Mode().IsRegular() || fileExists(out):
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
			return err
		}
		if err := copyFile(p, out, info, from, to); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

func copyFile(p, out string, info fs.FileInfo, from, to string) error {
	if from != "" && (strings.HasSuffix(p, ".toml") || strings.HasSuffix(p, ".json")) && info.Size() < 1<<20 {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		b = []byte(strings.ReplaceAll(string(b), from+string(filepath.Separator), to+string(filepath.Separator)))
		if err := os.WriteFile(out, b, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chtimes(out, info.ModTime(), info.ModTime())
	}
	in, err := os.Open(p)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, in); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return os.Chtimes(out, info.ModTime(), info.ModTime())
}

// PreferOwnCodexHome gives a new workspace login its own Codex home
// (OwnAgentHome) unless the shared default already holds
// conversations of this person, which `hearth agents isolate codex` moves.
// Returns the dir chosen ("" = unchanged).
func PreferOwnCodexHome(home string) string {
	ws := WorkspaceOf(home)
	if ws == "" || !agentEnabled(home, "codex") {
		return ""
	}
	if _, ok := AgentHomes(home)["codex"]; ok {
		return ""
	}
	roots := []string{filepath.Clean(ws)}
	if real, err := filepath.EvalSymlinks(ws); err == nil && real != ws {
		roots = append(roots, real)
	}
	files, _ := adapter.CodexAt(defaultAgentHome("codex")).Sessions()
	for _, f := range files {
		if cwd := sessionCwd(f); cwd != "" && under(cwd, roots) {
			return "" // theirs already: moving them is the user's call
		}
	}
	dst := OwnAgentHome(home, "codex")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return ""
	}
	if setLocalAgentHome(home, "codex", dst) != nil {
		return ""
	}
	os.Setenv("CODEX_HOME", dst)
	return dst
}

func agentEnabled(home, agent string) bool {
	for _, a := range EnabledAgents(home) {
		if a == agent {
			return true
		}
	}
	return false
}
