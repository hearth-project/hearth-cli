package client

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/backup"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/mcpcfg"
	"github.com/hearth-project/hearth-cli/internal/paths"
	"github.com/hearth-project/hearth-cli/internal/recipe"
	"github.com/hearth-project/hearth-cli/internal/tools"
	"github.com/hearth-project/hearth-cli/skills"
)

// Learning: the client inspects this user's workspace and home for
// credentials, configs, tools and conventions, asks the user what to keep,
// and records the answers as secrets plus steps of the synced recipe
// "learned" (and the user's "never ask again" choices). Every run refines
// the same recipe, so the restore flow improves as the user works.

// Finding is one thing worth restoring on a new machine.
type Finding struct {
	ID     string // stable; used for de-duplication and "never ask again"
	Kind   string // credential, config, env, tool, mcp, workspace
	Title  string
	Detail string
	// PerMachine suggests the item differs between kinds of machines
	// (GPU/driver paths, proxies, absolute paths outside home and workspace);
	// learn then offers to record it for this machine's profile only.
	PerMachine bool

	secretName  string
	secretPath  string       // file whose content becomes the secret
	secretFiles []secretFile // bundled credentials (an SSH identity has several)
	libFile     string       // library-relative file to copy srcFile into
	srcFile     string
	steps       []recipe.Step
	tool        *tools.Tool
	mcpImport   string          // mcpServers JSON to import
	wsFile      string          // workspace-relative path to sync (workspace adapter)
	libImport   *library.Target // agent dir whose skills / agents go into the library
}

// secretFile is one file of a bundled finding that becomes an encrypted
// secret (all of them are uploaded when the finding is accepted).
type secretFile struct{ name, path string }

// LearnedRecipe is the recipe learning writes to.
const LearnedRecipe = "learned"

// learnSettings is library/learn.json: per-user discovery settings.
type learnSettings struct {
	// TokenFiles are glob patterns (~/ and ${WORKSPACE}/ allowed) of login
	// token files to offer as credentials.
	TokenFiles []string `json:"token_files,omitempty"`
	// Identity tells `hearth login` how to find the user name in a local
	// token (company SSO caches) and which mail domain to use for it.
	Identity struct {
		EmailDomain   string `json:"email_domain,omitempty"`
		UsernameClaim string `json:"username_claim,omitempty"`
	} `json:"identity,omitempty"`
}

// learnConfig merges the site defaults' learn.json with the user's.
func learnConfig(home string) learnSettings {
	var out learnSettings
	for _, dir := range []string{SiteDir(home), LibraryDir(home)} {
		var s learnSettings
		loadJSON(filepath.Join(dir, "learn.json"), &s)
		out.TokenFiles = append(out.TokenFiles, s.TokenFiles...)
		if s.Identity.EmailDomain != "" {
			out.Identity.EmailDomain = s.Identity.EmailDomain
		}
		if s.Identity.UsernameClaim != "" {
			out.Identity.UsernameClaim = s.Identity.UsernameClaim
		}
	}
	return out
}

func learnIgnorePath(home string) string { return filepath.Join(LibraryDir(home), "learn-ignore.json") }

type learnIgnore struct {
	IDs []string `json:"ignore"`
}

// Ignored returns the IDs the user asked never to be asked about again
// (config.toml [learn] ignore, else the legacy file).
func Ignored(home string) map[string]bool {
	m := map[string]bool{}
	if c, err := UserConfig(home); err == nil && c.Learn != nil {
		for _, id := range c.Learn.Ignore {
			m[id] = true
		}
		return m
	}
	var f learnIgnore
	loadJSON(learnIgnorePath(home), &f)
	for _, id := range f.IDs {
		m[id] = true
	}
	return m
}

// IgnoreFinding records a permanent "no" (synced in config.toml).
func IgnoreFinding(home, id string) error {
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	if c.Learn == nil {
		c.Learn = &library.LearnConfig{}
	}
	for _, x := range c.Learn.Ignore {
		if x == id {
			return nil
		}
	}
	c.Learn.Ignore = append(c.Learn.Ignore, id)
	sort.Strings(c.Learn.Ignore)
	if err := SaveUserConfig(home, c); err != nil {
		return err
	}
	os.Remove(learnIgnorePath(home))
	return nil
}

func exists2(p string) bool { _, err := os.Stat(p); return err == nil }

var keyNameRE = regexp.MustCompile(`^id_[A-Za-z0-9_.-]+$`)

// identityFileRE matches an OpenSSH config "IdentityFile" line, with or
// without quotes. %-tokens and relative paths are ignored: they cannot be
// restored as plain files.
var identityFileRE = regexp.MustCompile(`(?mi)^\s*IdentityFile\s+(?:"([^"]+)"|(\S+))\s*$`)

func secretNameFor(p string) string {
	n := strings.Trim(regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(paths.Contract(p), "-"), "-")
	n = strings.TrimPrefix(strings.TrimPrefix(n, "WORKSPACE-"), "~-")
	if len(n) > 120 {
		n = n[len(n)-120:]
	}
	return "file-" + strings.TrimLeft(n, ".-")
}

// sshIdentityFinding turns a private ssh config into one coherent finding:
// the config, every private key it points at, and the wiring that makes
// git/ssh use it. Learning the pieces separately leaves a new machine with
// keys nobody references and ssh still reading the shared ~/.ssh/config.
func sshIdentityFinding(configPath string) (Finding, []string, bool) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Finding{}, nil, false
	}
	contract := paths.Contract(configPath)
	var steps []recipe.Step
	var keyPaths []string
	var files []secretFile
	seen := map[string]bool{}
	for _, m := range identityFileRE.FindAllStringSubmatch(string(data), -1) {
		raw := m[1]
		if raw == "" {
			raw = m[2]
		}
		if raw == "" || strings.Contains(raw, "%") {
			continue
		}
		p := paths.Expand(raw)
		if !filepath.IsAbs(p) {
			continue
		}
		key, err := os.ReadFile(p)
		if err != nil || !bytes.Contains(key, []byte("PRIVATE KEY")) {
			continue
		}
		c := paths.Contract(p)
		if seen[c] {
			continue
		}
		seen[c] = true
		keyPaths = append(keyPaths, c)
		files = append(files, secretFile{name: secretNameFor(p), path: p})
		steps = append(steps, recipe.Step{Type: "file", Name: "ssh-key:" + c,
			Secret: secretNameFor(p), To: []string{c}, Mode: "0600", Policy: "if-absent"})
	}
	steps = append(steps, recipe.Step{Type: "file", Name: "ssh-config",
		Library: "files/ssh_config", To: []string{contract}, Mode: "0600", Policy: "warn"})
	cmd := "ssh -F " + contract
	steps = append(steps, recipe.Step{Type: "env", Name: "env:GIT_SSH_COMMAND",
		Scope: []string{"${WORKSPACE}"},
		Set:   map[string]string{"GIT_SSH_COMMAND": cmd},
		Alias: map[string]string{"sshx": cmd, "scpx": "scp -F " + contract}})
	if host := firstSSHHost(string(data)); host != "" {
		steps = append(steps, recipe.Step{Type: "check", Name: "ssh-config-parses",
			Run: "ssh -F " + contract + " -G " + host, Expect: `(?m)^hostname .+`})
	}
	f := Finding{
		ID:          "ssh-identity:" + contract,
		Kind:        "identity",
		Title:       fmt.Sprintf("SSH identity (%d key(s) + private ssh config)", len(keyPaths)),
		Detail:      contract,
		secretFiles: files,
		libFile:     "files/ssh_config",
		srcFile:     configPath,
		steps:       steps,
	}
	return f, keyPaths, true
}

// firstSSHHost returns the first concrete Host in an ssh config (the
// wildcard "Host *" is only defaults, never a target to check).
func firstSSHHost(config string) string {
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(strings.ToLower(line), "host ")
		if !ok {
			continue
		}
		for _, h := range strings.Fields(rest) {
			if h != "*" && !strings.ContainsAny(h, "*?!") {
				return h
			}
		}
	}
	return ""
}

// fileSecret builds a finding that stores a file as a secret and restores it.
func fileSecret(id, kind, title, p string, policy string) Finding {
	to := paths.Contract(p)
	name := secretNameFor(p)
	return Finding{ID: id, Kind: kind, Title: title, Detail: to, secretName: name, secretPath: p,
		steps: []recipe.Step{{Type: "file", Name: id, Secret: name, To: []string{to}, Mode: "0600", Policy: policy}}}
}

// Discover inspects the workspace, home directory and environment.
func (e *Engine) Discover() []Finding {
	var out []Finding
	home, _ := os.UserHomeDir()
	ws := paths.Workspace()
	bases := []string{home}
	if ws != "" && ws != home {
		bases = append([]string{ws}, home)
	}
	seen := map[string]bool{}
	add := func(f Finding) {
		if !seen[f.ID] {
			seen[f.ID] = true
			out = append(out, f)
		}
	}

	// SSH identity first: it also claims the keys its config references, so
	// the key loop below does not offer them a second time.
	coveredKeys := map[string]bool{}
	coveredEnv := map[string]bool{}
	if ws != "" {
		if cfg := filepath.Join(ws, ".ssh", "config"); exists2(cfg) {
			if f, keys, ok := sshIdentityFinding(cfg); ok {
				add(f)
				for _, k := range keys {
					coveredKeys[k] = true
				}
				coveredEnv["GIT_SSH_COMMAND"] = true
			}
		}
	}

	// SSH keys next to the config (home keys stay separate: the same key at
	// a different path is a different restore target).
	for _, b := range bases {
		dir := filepath.Join(b, ".ssh")
		entries, _ := os.ReadDir(dir)
		for _, en := range entries {
			p := filepath.Join(dir, en.Name())
			if keyNameRE.MatchString(en.Name()) && !strings.HasSuffix(en.Name(), ".pub") {
				if coveredKeys[paths.Contract(p)] {
					continue
				}
				if data, err := os.ReadFile(p); err == nil && bytes.Contains(data, []byte("PRIVATE KEY")) {
					add(fileSecret("ssh-key:"+paths.Contract(p), "credential", "SSH private key "+en.Name(), p, "if-absent"))
				}
			}
		}
	}

	// Well-known credential files of agents and developer tools.
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	cands := []struct{ id, title, path, policy string }{
		{"codex-auth", "Codex login (auth.json)", filepath.Join(codexHome, "auth.json"), "if-absent"},
		{"codex-config", "Codex config.toml (model provider, may hold keys)", filepath.Join(codexHome, "config.toml"), "if-absent"},
		{"claude-credentials", "Claude Code login", filepath.Join(claudeHome, ".credentials.json"), "if-absent"},
		{"claude-settings", "Claude Code settings.json", filepath.Join(claudeHome, "settings.json"), "warn"},
		{"gh-hosts", "GitHub CLI login", filepath.Join(home, ".config/gh/hosts.yml"), "if-absent"},
		{"git-credentials", "git credential store", filepath.Join(home, ".git-credentials"), "if-absent"},
		{"netrc", ".netrc", filepath.Join(home, ".netrc"), "if-absent"},
	}
	if ws != "" {
		cands = append(cands, struct{ id, title, path, policy string }{"workspace-gitconfig", "git config of the workspace", filepath.Join(ws, ".gitconfig"), "warn"})
	}
	for _, c := range cands {
		if exists2(c.path) {
			add(fileSecret(c.id, "credential", c.title, c.path, c.policy))
		}
	}
	// Login tokens of tools that expire and are cheap to re-create, plus
	// the user's own patterns (library/learn.json "token_files", e.g. a
	// company SSO cache), synced so every machine looks for them.
	tokenGlobs := []string{filepath.Join(home, ".comate/login-user/*")}
	for _, g := range learnConfig(e.Home).TokenFiles {
		tokenGlobs = append(tokenGlobs, paths.Expand(g))
	}
	for _, pattern := range tokenGlobs {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			add(fileSecret("token:"+paths.Contract(m), "credential", "login token "+filepath.Base(m)+" (expires; restored only if absent)", m, "if-absent"))
		}
	}

	// Environment conventions of the current shell worth reproducing. Some
	// are habits that hold everywhere, others depend on the kind of machine.
	perMachineVars := map[string]bool{"HTTPS_PROXY": true, "https_proxy": true, "NO_PROXY": true, "CUDA_HOME": true,
		"HF_HOME": true, "NCCL_SOCKET_IFNAME": true, "NCCL_IB_HCA": true, "GLOO_SOCKET_IFNAME": true, "TRITON_CACHE_DIR": true}
	for _, v := range []string{"GIT_CONFIG_GLOBAL", "GIT_SSH_COMMAND", "VIMINIT", "npm_config_registry", "NPM_CONFIG_REGISTRY",
		"PIP_INDEX_URL", "UV_INDEX_URL", "HF_ENDPOINT", "HTTPS_PROXY", "https_proxy", "NO_PROXY",
		"CUDA_HOME", "HF_HOME", "NCCL_SOCKET_IFNAME", "NCCL_IB_HCA", "GLOO_SOCKET_IFNAME", "TRITON_CACHE_DIR"} {
		if coveredEnv[v] {
			continue
		}
		val := os.Getenv(v)
		if val == "" {
			continue
		}
		pv := contractWords(val)
		st := recipe.Step{Type: "env", Name: "env:" + v, Set: map[string]string{v: pv}}
		if ws != "" {
			st.Scope = []string{"${WORKSPACE}"}
		}
		add(Finding{ID: "env:" + v, Kind: "env", Title: "environment variable " + v, Detail: pv, steps: []recipe.Step{st},
			PerMachine: perMachineVars[v] || hasForeignPath(pv)})
	}

	// Agent CLIs: pin the installed versions.
	for _, name := range tools.BuiltinNames() {
		t := tools.Tool{Name: name}
		if v := tools.Installed(t); v != "" && v != "unknown" {
			t.Version = v
			add(Finding{ID: "tool:" + name, Kind: "tool", Title: name + " CLI", Detail: "version " + v, tool: &t})
		}
	}
	if p, err := exec.LookPath("codex-mixin"); err == nil && !SiteCaptures(e.Home, "codex-mixin-config") {
		out, _ := exec.Command(p, "--version").Output()
		add(Finding{ID: "codex-mixin", Kind: "tool", Title: "codex-mixin gateway (its config is backed up, so new machines skip the QR login)",
			Detail: strings.TrimSpace(string(out)), steps: codexMixinSteps()})
	}

	// MCP servers configured outside hearth.
	cfg, _, _ := LoadMCPConfig(e.Home)
	mcpSources := []struct{ id, path string }{{"mcp:comate", filepath.Join(home, ".comate/mcp.json")}, {"mcp:claude", filepath.Join(home, ".claude.json")}}
	if ws != "" && ws != home {
		mcpSources = append(mcpSources, struct{ id, path string }{"mcp:ws-comate", filepath.Join(ws, ".comate/mcp.json")},
			struct{ id, path string }{"mcp:ws", filepath.Join(ws, ".mcp.json")})
	}
	for _, src := range mcpSources {
		servers, _, err := mcpcfg.ImportJSON(src.path)
		if err != nil {
			continue
		}
		var fresh []string
		for n := range servers {
			if _, ok := cfg.Servers[n]; !ok {
				fresh = append(fresh, n)
			}
		}
		if len(fresh) > 0 {
			sort.Strings(fresh)
			add(Finding{ID: src.id, Kind: "mcp", Title: "MCP servers in " + paths.Contract(src.path), Detail: strings.Join(fresh, ", "), mcpImport: src.path})
		}
	}

	// Agent directories of the workspace (project-level .codex / .claude /
	// .comate): skills and agents not in the library yet, and personal files.
	if ws != "" && ws != home {
		lib := library.Library{Root: LibraryDir(e.Home)}
		for _, t := range []library.Target{library.CodexTarget(filepath.Join(ws, ".codex")), library.ClaudeTarget(filepath.Join(ws, ".claude")),
			library.ComateTarget(filepath.Join(ws, ".comate"))} {
			if n, names := missingFromLibrary(lib, t); n > 0 {
				add(Finding{ID: "lib:" + paths.Contract(t.Root), Kind: "library", Title: fmt.Sprintf("%d skill(s)/agent(s) in %s not in your library", n, paths.Contract(t.Root)),
					Detail: names, libImport: &t})
			}
		}
		for _, f := range discoverWorkspaceFiles(e.Home, ws) {
			add(f)
		}
	}

	disambiguateSecrets(out)
	return e.filterBySelection(out)
}

// filterBySelection drops findings the user's backup selection rules out:
// credentials unless @credentials is on, and excluded paths.
func (e *Engine) filterBySelection(fs []Finding) []Finding {
	cfg := BackupSelection(e.Home)
	var out []Finding
	for _, f := range fs {
		// Credentials are offered until the user says never (!@credentials).
		if f.Kind == "credential" && cfg.Denied[backup.Credentials] {
			continue
		}
		if f.secretPath != "" && cfg.Excluded(f.secretPath) {
			continue
		}
		if f.srcFile != "" && cfg.Excluded(f.srcFile) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// disambiguateSecrets gives file secrets their own names when two found
// files would share one but differ (e.g. ${WORKSPACE}/.ssh/id_rsa and
// ~/.ssh/id_rsa are different keys). Identical copies keep sharing.
func disambiguateSecrets(fs []Finding) {
	first := map[string]int{}
	for i := range fs {
		f := &fs[i]
		if f.secretName == "" {
			continue
		}
		j, ok := first[f.secretName]
		if !ok {
			first[f.secretName] = i
			continue
		}
		a, err1 := os.ReadFile(fs[j].secretPath)
		b, err2 := os.ReadFile(f.secretPath)
		if err1 == nil && err2 == nil && bytes.Equal(a, b) {
			continue
		}
		old := f.secretName
		f.secretName = old + ".home"
		if strings.HasPrefix(paths.Contract(f.secretPath), "${WORKSPACE}") {
			f.secretName = old + ".ws"
		}
		for k := range f.steps {
			if f.steps[k].Secret == old {
				f.steps[k].Secret = f.secretName
			}
		}
	}
}

// missingFromLibrary counts skills and agents of an agent directory that
// the library does not have.
func missingFromLibrary(lib library.Library, t library.Target) (int, string) {
	var names []string
	subs := []string{"skills", "agents"}
	if t.SkillsOnly() {
		subs = subs[:1] // Comate agents are not imported yet
	}
	for _, sub := range subs {
		entries, _ := os.ReadDir(filepath.Join(t.Root, sub))
		for _, en := range entries {
			n := en.Name()
			if sub == "agents" {
				ext := ".md"
				if t.Name == "codex" {
					ext = ".toml"
				}
				if en.IsDir() || !strings.HasSuffix(n, ext) {
					continue // companion directories, other files
				}
				n = strings.TrimSuffix(n, ext)
			}
			if strings.HasPrefix(n, ".") || !library.ValidName(n) || strings.EqualFold(n, "readme") {
				continue
			}
			if sub == "skills" {
				md, err := os.ReadFile(filepath.Join(t.Root, sub, en.Name(), "SKILL.md"))
				if err != nil || skills.Bundled(md) {
					continue
				}
			}
			dst := filepath.Join(lib.Root, sub, n)
			if sub == "skills" {
				dst = filepath.Join(lib.SkillsDir(), n)
			}
			if sub == "agents" {
				dst += ".md"
			}
			if _, err := os.Stat(dst); err != nil {
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	detail := strings.Join(names, ", ")
	if len(detail) > 120 {
		detail = detail[:117] + "..."
	}
	return len(names), detail
}

// hasForeignPath reports an absolute path outside home and the workspace
// (e.g. /usr/local/cuda-12.4, /mnt/nvme0): likely specific to this machine.
func hasForeignPath(v string) bool {
	for _, w := range strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ':' || r == '=' }) {
		if strings.HasPrefix(w, "/") && w != "/" && !strings.HasPrefix(w, "//") { // "//host" is the rest of a URL
			return true
		}
	}
	return false
}

func contractWords(v string) string {
	words := strings.Split(v, " ")
	for i, w := range words {
		if filepath.IsAbs(w) {
			words[i] = paths.Contract(w)
		}
	}
	return strings.Join(words, " ")
}

func learnSnoozePath(home string) string { return filepath.Join(home, "learn-snooze.json") }

// SnoozeLearn stops the "not recorded yet" reminder on this machine for d.
func SnoozeLearn(home string, d time.Duration) error {
	return saveJSON(learnSnoozePath(home), map[string]int64{"until": time.Now().Add(d).Unix()})
}

// LearnSnoozed reports whether the reminder is snoozed now.
func LearnSnoozed(home string) bool {
	var m map[string]int64
	return loadJSON(learnSnoozePath(home), &m) == nil && m["until"] > time.Now().Unix()
}

// codexMixinSteps keep codex-mixin's configuration (provider credentials
// from its QR-code login, selected models) as an encrypted secret and
// import it on new machines, so only the first machine scans a QR code.
// `config export` / `import` take a file path, hence the temp files.
func codexMixinSteps() []recipe.Step {
	// The installer (or a site recipe) puts it in ~/.local/bin, which a
	// fresh shell has on PATH but the setup process may not.
	const p = `PATH="$HOME/.local/bin:$PATH"; `
	return []recipe.Step{
		{Type: "capture", Name: "codex-mixin-export", Secret: "codex-mixin-config", Every: "6h",
			Run: p + `t=$(mktemp) && codex-mixin config export --no-tui "$t" >/dev/null && cat "$t"; r=$?; rm -f "$t"; exit $r`},
		{Type: "manual", Name: "codex-mixin-install", Text: "install codex-mixin into ~/.local/bin (the release for `uname -m`; guide: hearth docs env, section 5)",
			DoneWhen: p + "command -v codex-mixin"},
		{Type: "run", Name: "codex-mixin-import", WhenSecret: "codex-mixin-config",
			Unless: p + "codex-mixin doctor --no-tui | grep -q 'doctor: ok'",
			Run:    p + `command -v codex-mixin >/dev/null || { echo "codex-mixin not installed"; exit 1; }; t=$(mktemp) && hearth secret get codex-mixin-config > "$t" && codex-mixin config import --no-tui "$t" && codex-mixin config apply --no-tui && codex-mixin service restart --no-tui; r=$?; rm -f "$t"; exit $r`},
		{Type: "manual", Name: "codex-mixin-setup", UnlessSecret: "codex-mixin-config", TTY: true,
			Text: "first machine only, in the user's own terminal: codex-mixin setup (its login may show a QR code; guide: hearth docs env, section 5), then: hearth save --yes"},
	}
}
