package client

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/paths"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// Shell environment: the user's own lines of ~/.bashrc and ~/.zshrc (the
// parts that differ from the distribution's default in /etc/skel) and the
// whole ~/.bash_aliases are kept as encrypted secrets ("shell-bashrc", ...;
// rc files often export API keys). A new machine gets them as a snippet
// under <hearth home>/shell, sourced from its own rc file by a managed
// block, so nothing of that machine's rc is replaced.
//
// Several machines add to the same snippet: an upload merges this machine's
// paragraphs into the stored ones and drops only the paragraphs this
// machine removed since its last upload. With a workspace on a shared
// account the snippet is sourced only once the shell is inside the
// workspace (the rc file is everyone's).

const maxShellBytes = 256 << 10

// ShellFile is one rc file Hearth keeps.
type ShellFile struct {
	Path   string // ~/.bashrc
	Secret string // shell-bashrc
	Hook   string // rc file whose managed block sources the snippet
	Whole  bool   // the whole file is the user's (no distribution default)
}

func shellOffHere(home string) string { return filepath.Join(home, "shell-off-here") }

// shellDisabledHere: the user took Hearth's lines out of this machine's rc
// files (hearth shell uninstall, or commented out the block's line), so
// nothing puts them back until hearth shell restore.
func shellDisabledHere(home string) bool {
	if _, err := os.Stat(shellOffHere(home)); err == nil {
		return true
	}
	for _, f := range ShellFiles(home) {
		b, err := os.ReadFile(paths.Expand(f.Hook))
		if err != nil {
			continue
		}
		hook := f.hookPath(home)
		for _, l := range strings.Split(string(b), "\n") {
			if t := strings.TrimSpace(l); strings.HasPrefix(t, "#") && strings.Contains(t, hook) && strings.Contains(t, ". ") {
				return true // the block's source line commented out by hand
			}
		}
	}
	return false
}

// ShellOn reports whether [shell] sync is on (default true).
func ShellOn(home string) bool {
	c, err := UserConfig(home)
	if err != nil || c.Shell == nil || c.Shell.Sync == nil {
		return true
	}
	return *c.Shell.Sync
}

// ShellFiles lists the rc files kept ([shell] files, else the defaults).
func ShellFiles(home string) []ShellFile {
	list := []string{"~/.bashrc", "~/.zshrc", "~/.bash_aliases"}
	if c, err := UserConfig(home); err == nil && c.Shell != nil && len(c.Shell.Files) > 0 {
		list = c.Shell.Files
	}
	var out []ShellFile
	for _, p := range list {
		base := strings.TrimPrefix(filepath.Base(p), ".")
		f := ShellFile{Path: p, Secret: "shell-" + regexp.MustCompile(`[^A-Za-z0-9_-]+`).ReplaceAllString(base, "-")}
		switch base {
		case "zshrc", "zprofile":
			f.Hook = "~/.zshrc"
		default:
			f.Hook = "~/.bashrc"
		}
		f.Whole = base == "bash_aliases" || base == "aliases"
		if runtime.GOOS != "linux" {
			// Kept per operating system: lines of a Linux rc (paths under
			// /root, JAVA_HOME of a cluster image, HOME itself) break a Mac.
			f.Secret += "." + runtime.GOOS
		}
		out = append(out, f)
	}
	return out
}

func shellDir(home string) string { return filepath.Join(home, "shell") }

// snippetPath is the restored shared content; hookPath the script the rc
// block sources (it sources the snippet, scoped to the workspace if any).
func (f ShellFile) snippetPath(home string) string {
	return filepath.Join(shellDir(home), strings.TrimPrefix(f.Secret, "shell-")+".sh")
}
func (f ShellFile) hookPath(home string) string {
	return filepath.Join(shellDir(home), strings.TrimPrefix(f.Secret, "shell-")+".hook.sh")
}

var hearthBlockRE = regexp.MustCompile(`(?ms)^# >>> hearth[^\n]*\n.*?^# <<< hearth[^\n]*(\n|$)`)

// stripHearth removes Hearth's own blocks and lines.
func stripHearth(s string) string {
	s = hearthBlockRE.ReplaceAllString(s, "")
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "added by the hearth installer") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

// OwnShell returns the user's own part of an rc file ("" if none).
func (f ShellFile) OwnShell() string {
	b, err := os.ReadFile(paths.Expand(f.Path))
	if err != nil || len(b) > maxShellBytes {
		return ""
	}
	text := toolBlockRE.ReplaceAllString(stripHearth(string(b)), "")
	if f.Whole {
		return strings.TrimSpace(text)
	}
	skel, err := os.ReadFile(filepath.Join("/etc/skel", filepath.Base(f.Path)))
	if err != nil {
		return strings.TrimSpace(text) // no distribution default: all of it is the user's
	}
	def := map[string]bool{}
	for _, u := range shellUnits(string(skel)) {
		def[normUnit(u)] = true
	}
	var own []string
	for _, u := range shellUnits(text) {
		if n := normUnit(u); !def[n] && !distroDefault(n) {
			own = append(own, u)
		}
	}
	return strings.Join(meaningful(own), "\n\n")
}

// toolBlockRE matches blocks that installers manage in rc files
// ("# >>> conda initialize >>>" ... "# <<< conda initialize <<<"): each
// machine's own install writes its own.
var toolBlockRE = regexp.MustCompile(`(?ms)^# >>> [^\n]*>>>\s*\n.*?^# <<< [^\n]*<<<[^\n]*(\n|$)`)

// defaultMarkers identify statements of the stock rc files of common
// distributions (older or newer than this machine's /etc/skel).
var defaultMarkers = []string{"debian_chroot", "color_prompt", "lesspipe", "dircolors", "bash_completion",
	"HISTCONTROL=", "HISTSIZE=", "HISTFILESIZE=", "shopt -s histappend", "shopt -s checkwinsize", "shopt -s globstar",
	`[ -z "$PS1" ] && return`, "case $- in", "xterm*|rxvt*", "alias ll=", "alias la=", "alias l=", "alias ls='ls --color",
	"alias grep='grep --color", "alias fgrep=", "alias egrep=", "alias dir=", "alias vdir=", "alias alert=", ".bash_aliases",
	"GCC_COLORS", "/etc/bashrc", "/etc/bash.bashrc"}

func distroDefault(u string) bool {
	for _, m := range defaultMarkers {
		if strings.Contains(u, m) {
			return true
		}
	}
	return false
}

// normUnit compares units without comments and indentation.
func normUnit(u string) string {
	var out []string
	for _, l := range strings.Split(u, "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			out = append(out, strings.Join(strings.Fields(t), " "))
		}
	}
	return strings.Join(out, "\n")
}

var wordSepRE = regexp.MustCompile(`[\s;&|()]+`)

// shellUnits splits shell text into top-level statements: a compound
// command (if / case / loops / { } and function bodies) is one unit with
// all its lines, so a unit can be sourced on its own. Comment lines right
// above a statement belong to it; blank lines separate units.
func shellUnits(text string) []string {
	var units, cur []string
	depth := 0
	var quote byte // inside a multi-line ' or " string
	cont := false  // previous line ended with a backslash
	flush := func() {
		if len(cur) > 0 {
			units = append(units, strings.TrimRight(strings.Join(cur, "\n"), "\n "))
			cur = nil
		}
	}
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		idle := depth == 0 && quote == 0 && !cont
		if idle && t == "" {
			flush()
			continue
		}
		cur = append(cur, l)
		if idle && strings.HasPrefix(t, "#") {
			continue
		}
		// Unquoted code of the line, without its comment.
		var code strings.Builder
		for k := 0; k < len(l); k++ {
			ch := l[k]
			switch {
			case quote == '\'':
				if ch == '\'' {
					quote = 0
				}
			case quote == '"':
				if ch == '\\' {
					k++
				} else if ch == '"' {
					quote = 0
				}
			case ch == '\\':
				k++
				if k >= len(l) {
					code.WriteByte('\\')
				}
			case ch == '\'' || ch == '"':
				quote = ch
				code.WriteByte(' ')
			case ch == '#' && (k == 0 || strings.IndexByte(" \t;", l[k-1]) >= 0):
				k = len(l)
			default:
				code.WriteByte(ch)
			}
		}
		c := code.String()
		cont = quote == 0 && strings.HasSuffix(c, "\\")
		for _, w := range wordSepRE.Split(c, -1) {
			switch w {
			case "if", "case", "for", "while", "until", "select", "{":
				depth++
			case "fi", "esac", "done", "}":
				if depth > 0 {
					depth--
				}
			default:
				if strings.HasSuffix(w, "{") && !strings.Contains(w, "$") {
					depth++ // f(){
				}
			}
		}
		if depth == 0 && quote == 0 && !cont {
			flush()
		}
	}
	flush()
	return units
}

// meaningful drops hunks made only of comments and blank lines.
func meaningful(hunks []string) []string {
	var out []string
	for _, h := range hunks {
		for _, l := range strings.Split(h, "\n") {
			if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
				out = append(out, strings.TrimSpace(h))
				break
			}
		}
	}
	return out
}

// paragraphs splits text into top-level statements (shellUnits).
func paragraphs(s string) []string {
	var out []string
	for _, p := range shellUnits(s) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// mergeShell: base's paragraphs, then the new ones of each add, minus the
// paragraphs in drop.
func mergeShell(base string, drop map[string]bool, adds ...string) string {
	var out []string
	seen := map[string]bool{}
	for _, src := range append([]string{base}, adds...) {
		for _, p := range paragraphs(src) {
			if !seen[p] && !drop[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n\n") + "\n"
}

type shellState struct {
	// Per secret: what this machine's rc and snippet held at the last
	// upload or restore (to see what the user changed since).
	Files map[string]shellFileState `json:"files"`
}

type shellFileState struct {
	Own     string `json:"own_sha256"`
	Snippet string `json:"snippet_sha256"`
	// Paragraphs of the last state, so removals can be told from paragraphs
	// another machine added; stored as hashes (no content on disk twice).
	OwnParas     []string `json:"own_paragraphs,omitempty"`
	SnippetParas []string `json:"snippet_paragraphs,omitempty"`
}

func shellStatePath(home string) string { return filepath.Join(home, "shell-state.json") }

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func paraHashes(s string) []string {
	var out []string
	for _, p := range paragraphs(s) {
		out = append(out, sha(p))
	}
	return out
}

func loadShellState(home string) shellState {
	var st shellState
	if err := loadJSON(shellStatePath(home), &st); err != nil && !errors.Is(err, os.ErrNotExist) {
		st = shellState{}
	}
	if st.Files == nil {
		st.Files = map[string]shellFileState{}
	}
	return st
}

// ShellSaved is one rc file SaveShell uploaded.
type ShellSaved struct {
	File       ShellFile
	Paragraphs int
}

// SaveShell uploads the rc files whose own part (or restored snippet)
// changed since this machine's last upload; nothing otherwise, so it is
// cheap to run from the daemon.
func (e *Engine) SaveShell() ([]ShellSaved, error) {
	if !ShellOn(e.Home) {
		return nil, nil
	}
	st := loadShellState(e.Home)
	var stored map[string][]byte
	var out []ShellSaved
	changed := false
	for _, f := range ShellFiles(e.Home) {
		own := f.OwnShell()
		snip := ""
		if b, err := os.ReadFile(f.snippetPath(e.Home)); err == nil {
			snip = withoutDerived(string(b))
		}
		prev := st.Files[f.Secret]
		if sha(own) == prev.Own && sha(snip) == prev.Snippet {
			continue
		}
		if strings.TrimSpace(own) == "" && strings.TrimSpace(snip) == "" && prev.Own == "" && prev.Snippet == "" {
			continue
		}
		if stored == nil {
			stored = map[string][]byte{}
			all, err := e.Secrets()
			if err != nil {
				return out, err
			}
			for _, s := range all {
				stored[s.Name] = s.Value
			}
		}
		drop := map[string]bool{}
		for _, pair := range []struct {
			hashes []string
			cur    string
		}{{prev.OwnParas, own}, {prev.SnippetParas, snip}} {
			now := map[string]bool{}
			for _, h := range paraHashes(pair.cur) {
				now[h] = true
			}
			for _, h := range pair.hashes {
				if !now[h] {
					drop[h] = true
				}
			}
		}
		base := string(stored[f.Secret])
		var keep []string
		for _, p := range paragraphs(base) {
			if !drop[sha(p)] {
				keep = append(keep, p)
			}
		}
		merged := mergeShell(strings.Join(keep, "\n\n"), nil, snip, own)
		if merged != base {
			if strings.TrimSpace(merged) == "" {
				if base != "" {
					if err := e.DeleteSecret(f.Secret); err != nil {
						return out, err
					}
				}
			} else if err := e.SetSecret(Secret{Name: f.Secret, Value: []byte(merged)}); err != nil {
				return out, err
			}
			out = append(out, ShellSaved{File: f, Paragraphs: len(paragraphs(merged))})
		}
		st.Files[f.Secret] = shellFileState{Own: sha(own), Snippet: sha(snip), OwnParas: paraHashes(own), SnippetParas: paraHashes(snip)}
		changed = true
	}
	if changed {
		return out, saveJSON(shellStatePath(e.Home), st)
	}
	return out, nil
}

var sourceLineRE = regexp.MustCompile(`^(\s*)(?:source|\.)\s+["']?([^"'\s;&|]+)["']?\s*$`)

// Version managers and prompt tools people call from their rc files; a new
// machine often lacks them, and every new shell would print "command not
// found". Their lines are guarded with command -v (checked when the shell
// starts, so they work again once the tool is installed).
var guardedTools = map[string]bool{"conda": true, "mamba": true, "micromamba": true, "pyenv": true, "rbenv": true, "nodenv": true,
	"nvm": true, "fnm": true, "direnv": true, "starship": true, "zoxide": true, "atuin": true, "thefuck": true, "rustup": true, "brew": true}

var (
	toolLineRE = regexp.MustCompile(`^(\s*)([a-z][a-z0-9_-]*)(\s[^\\]*)?$`)
	evalToolRE = regexp.MustCompile(`^(\s*)eval\s+"\$\(([a-z][a-z0-9_-]*)\s[^"]*\)"\s*$`)
)

// guardTool returns the line guarded by command -v when it calls one of
// guardedTools that is not installed here ("" = leave it).
func guardTool(l string) string {
	if strings.Contains(l, "<<") || strings.HasSuffix(strings.TrimSpace(l), "{") {
		return ""
	}
	m := evalToolRE.FindStringSubmatch(l)
	if m == nil {
		m = toolLineRE.FindStringSubmatch(l)
	}
	if m == nil || !guardedTools[m[2]] {
		return ""
	}
	if _, err := exec.LookPath(m[2]); err == nil {
		return ""
	}
	return m[1] + "command -v " + m[2] + " >/dev/null 2>&1 && " + strings.TrimLeft(l, " \t") + " # hearth: guarded, " + m[2] + " was not installed here"
}

// identityVarRE matches assignments of variables that belong to the
// machine and account (an rc line "export HOME=/root" from a Linux box
// makes ~ point to /root on a Mac).
var identityVarRE = regexp.MustCompile(`^(\s*)(export\s+)?(HOME|USER|LOGNAME|SHELL|PWD|OLDPWD|HOSTNAME|TMPDIR)=`)

var varRefRE = regexp.MustCompile(`^\$\{?[A-Za-z_][A-Za-z0-9_]*\}?$`)

var assignRE = regexp.MustCompile(`^(\s*)(export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(\S*)\s*$`)

// guardPaths handles assignments naming absolute paths that do not exist
// here (JAVA_HOME=/opt/cluster/jdk, PATH=/root/.tool/bin:$PATH): list
// variables lose the missing entries (or the line, when nothing of this
// machine is left); others are set only if the path exists when the shell
// starts. ok is false when the line is left as it is.
func guardPaths(l string) (string, bool) {
	m := assignRE.FindStringSubmatch(l)
	if m == nil || strings.ContainsAny(m[4], "`;&|*") || strings.Contains(m[4], "$(") {
		return "", false
	}
	val, q := m[4], ""
	if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
		val, q = val[1:len(val)-1], val[:1]
	}
	if strings.ContainsAny(val, "\"'") {
		return "", false
	}
	h, _ := os.UserHomeDir()
	parts := strings.Split(val, ":")
	var keep, missing []string
	for _, p := range parts {
		x := p
		for _, pre := range []string{"${HOME}", "$HOME", "~"} {
			if strings.HasPrefix(x, pre) && h != "" {
				x = h + x[len(pre):]
				break
			}
		}
		if strings.HasPrefix(x, "/") && !strings.Contains(x, "$") {
			if _, err := os.Stat(x); err != nil {
				missing = append(missing, p)
				continue
			}
		}
		keep = append(keep, p)
	}
	if len(missing) == 0 {
		return "", false
	}
	note := " # hearth: not on this machine: " + strings.Join(missing, ", ")
	if len(parts) > 1 { // a list: PATH, LD_LIBRARY_PATH, PYTHONPATH, ...
		onlyRefs := true
		for _, p := range keep {
			if !varRefRE.MatchString(p) {
				onlyRefs = false // something of this machine besides $PATH itself
			}
		}
		if onlyRefs {
			return m[1] + ":" + note + ": " + strings.TrimSpace(l), true
		}
		return m[1] + m[2] + m[3] + "=" + q + strings.Join(keep, ":") + q + note, true
	}
	return m[1] + "[ -e " + shQuote(missing[0]) + " ] && " + strings.TrimLeft(l, " \t") + note, true
}

func shQuote(p string) string {
	for _, pre := range []string{"${HOME}", "$HOME", "~"} {
		if rest, ok := strings.CutPrefix(p, pre); ok {
			return "\"$HOME\"'" + strings.ReplaceAll(rest, "'", `'"'"'`) + "'"
		}
	}
	return "'" + strings.ReplaceAll(p, "'", `'"'"'`) + "'"
}

// neutralize replaces lines that source a file missing on this machine
// with a no-op (":" keeps if / fi bodies valid), and guards calls of tools
// that are not installed here.
func neutralize(s string) (string, []string) {
	var missing []string
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if m := identityVarRE.FindStringSubmatch(l); m != nil {
			lines[i] = m[1] + ": # hearth: " + m[3] + " is this machine's own, not restored: " + strings.TrimSpace(l)
			continue
		}
		if g := guardTool(l); g != "" {
			lines[i] = g
			continue
		}
		if g, ok := guardPaths(l); ok {
			lines[i] = g
			continue
		}
		m := sourceLineRE.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		p := strings.NewReplacer("$HOME", os.Getenv("HOME"), "${HOME}", os.Getenv("HOME")).Replace(m[2])
		p = paths.Expand(p)
		if !filepath.IsAbs(p) || strings.ContainsAny(p, "$`*") {
			continue
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			lines[i] = m[1] + ": # hearth: not on this machine: " + strings.TrimSpace(l)
			missing = append(missing, m[2])
		}
	}
	return strings.Join(lines, "\n"), missing
}

// ShellRestored is one rc file RestoreShell set up.
type ShellRestored struct {
	File       ShellFile
	Paragraphs int
	Missing    []string // sourced files not on this machine (left out)
	Hooked     string   // rc file that sources it
}

// RestoreShell writes the stored rc content of each file as a snippet
// (paragraphs this machine's rc already has left out) and sources it from
// the rc file. The rc file itself only gets the managed block.
func (e *Engine) RestoreShell() ([]ShellRestored, error) {
	if !ShellOn(e.Home) || shellDisabledHere(e.Home) {
		return nil, nil
	}
	all, err := e.Secrets()
	if err != nil {
		return nil, err
	}
	stored := map[string][]byte{}
	for _, s := range all {
		stored[s.Name] = s.Value
	}
	st := loadShellState(e.Home)
	ws := paths.Workspace()
	h, _ := os.UserHomeDir()
	shared := ws != "" && ws != h
	var out []ShellRestored
	for _, f := range ShellFiles(e.Home) {
		if base, _, cut := strings.Cut(f.Secret, "."); cut {
			// Older versions sourced the Linux lines here too: take them out.
			legacy := ShellFile{Path: f.Path, Secret: base, Hook: f.Hook}
			if changed, _ := recipe.UninstallRC(paths.Expand(f.Hook), legacy.hookPath(e.Home)); changed {
				_ = os.Remove(legacy.hookPath(e.Home))
				_ = os.Remove(legacy.snippetPath(e.Home))
			}
		}
		v, ok := stored[f.Secret]
		if !ok {
			continue
		}
		hook := paths.Expand(f.Hook)
		if f.Hook == "~/.zshrc" {
			if _, err := os.Stat(hook); err != nil && filepath.Base(os.Getenv("SHELL")) != "zsh" {
				continue // no zsh here
			}
		}
		own := ""
		if b, err := os.ReadFile(paths.Expand(f.Path)); err == nil {
			own = stripHearth(string(b))
		}
		var keep []string
		for _, p := range paragraphs(string(v)) {
			if !strings.Contains(own, p) {
				keep = append(keep, p)
			}
		}
		text, missing := neutralize(strings.Join(keep, "\n\n"))
		r := ShellRestored{File: f, Paragraphs: len(keep), Missing: missing, Hooked: paths.Contract(hook)}
		if err := os.MkdirAll(shellDir(e.Home), 0o700); err != nil {
			return out, err
		}
		body := "# Restored by hearth from your other machines (" + f.Path + "). Edits here are kept and synced.\n" + text + "\n"
		if len(keep) == 0 {
			body = ""
		}
		if err := writeFileAtomic(f.snippetPath(e.Home), []byte(body), 0o600); err != nil {
			return out, err
		}
		if err := writeFileAtomic(f.hookPath(e.Home), shellHook(f.snippetPath(e.Home), ws, shared, f.Secret), 0o600); err != nil {
			return out, err
		}
		if _, err := recipe.InstallRC(hook, f.hookPath(e.Home)); err != nil {
			return out, err
		}
		st.Files[f.Secret] = shellFileState{Own: sha(f.OwnShell()), Snippet: sha(withoutDerived(body)), OwnParas: paraHashes(f.OwnShell()), SnippetParas: paraHashes(withoutDerived(body))}
		out = append(out, r)
	}
	return out, saveJSON(shellStatePath(e.Home), st)
}

// withoutDerived drops the paragraphs Hearth changed for this machine
// (marked "# hearth:"), so a snippet edited here never uploads them in
// place of the originals.
func withoutDerived(s string) string {
	var keep []string
	for _, p := range paragraphs(s) {
		if !strings.Contains(p, "# hearth:") {
			keep = append(keep, p)
		}
	}
	if len(keep) == 0 {
		return ""
	}
	return strings.Join(keep, "\n\n") + "\n"
}

// RefreshShell applies the current guards to the snippets restored here
// (by older versions too) and, off Linux, removes the Linux lines older
// versions sourced. The daemon runs it; no download, no rc change otherwise.
func (e *Engine) RefreshShell() (int, error) {
	if shellDisabledHere(e.Home) {
		return 0, nil
	}
	st := loadShellState(e.Home)
	n := 0
	for _, f := range ShellFiles(e.Home) {
		if base, _, cut := strings.Cut(f.Secret, "."); cut {
			legacy := ShellFile{Path: f.Path, Secret: base, Hook: f.Hook}
			if changed, _ := recipe.UninstallRC(paths.Expand(f.Hook), legacy.hookPath(e.Home)); changed {
				_ = os.Remove(legacy.hookPath(e.Home))
				_ = os.Remove(legacy.snippetPath(e.Home))
				n++
			}
		}
		b, err := os.ReadFile(f.snippetPath(e.Home))
		if err != nil {
			continue
		}
		text, _ := neutralize(string(b))
		if text == string(b) {
			continue
		}
		if err := writeFileAtomic(f.snippetPath(e.Home), []byte(text), 0o600); err != nil {
			return n, err
		}
		prev := st.Files[f.Secret]
		prev.Snippet, prev.SnippetParas = sha(withoutDerived(text)), paraHashes(withoutDerived(text))
		st.Files[f.Secret] = prev
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, saveJSON(shellStatePath(e.Home), st)
}

// UninstallShell takes Hearth's lines out of this machine's rc files: the
// managed blocks and the restored snippets (the stored copy stays; turn
// syncing off with [shell] sync = false).
func (e *Engine) UninstallShell() ([]string, error) {
	var done []string
	if err := os.WriteFile(shellOffHere(e.Home), []byte("hearth shell uninstall: rc lines are not restored here until hearth shell restore\n"), 0o600); err != nil {
		return nil, err
	}
	for _, f := range ShellFiles(e.Home) {
		cands := []ShellFile{f}
		if base, _, cut := strings.Cut(f.Secret, "."); cut {
			cands = append(cands, ShellFile{Path: f.Path, Secret: base, Hook: f.Hook})
		}
		for _, c := range cands {
			changed, err := recipe.UninstallRC(paths.Expand(c.Hook), c.hookPath(e.Home))
			if err != nil {
				return done, err
			}
			_ = os.Remove(c.hookPath(e.Home))
			_ = os.Remove(c.snippetPath(e.Home))
			if changed {
				done = append(done, c.Hook)
			}
		}
	}
	return done, nil
}

// EnableShellHere undoes UninstallShell for this machine.
func (e *Engine) EnableShellHere() { _ = os.Remove(shellOffHere(e.Home)) }

// PreviewShell returns, per rc file, what RestoreShell would write here.
func (e *Engine) PreviewShell() (map[string]string, error) {
	all, err := e.Secrets()
	if err != nil {
		return nil, err
	}
	stored := map[string][]byte{}
	for _, s := range all {
		stored[s.Name] = s.Value
	}
	out := map[string]string{}
	for _, f := range ShellFiles(e.Home) {
		v, ok := stored[f.Secret]
		if !ok {
			continue
		}
		own := ""
		if b, err := os.ReadFile(paths.Expand(f.Path)); err == nil {
			own = stripHearth(string(b))
		}
		var keep []string
		for _, p := range paragraphs(string(v)) {
			if !strings.Contains(own, p) {
				keep = append(keep, p)
			}
		}
		text, _ := neutralize(strings.Join(keep, "\n\n"))
		out[f.Path] = text
	}
	return out, nil
}

// shellHook sources the snippet; on a shared account only once the shell
// is inside the workspace (checked at every prompt until then).
func shellHook(snippet, ws string, shared bool, id string) []byte {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	if !shared {
		return []byte(fmt.Sprintf("# Generated by hearth.\n[ -f %s ] && . %s\n", q(snippet), q(snippet)))
	}
	fn := "_hearth_" + strings.ReplaceAll(id, "-", "_")
	return []byte(fmt.Sprintf(`# Generated by hearth: your shell lines, only inside your workspace.
%[1]s() {
  case "$PWD" in
    %[2]s|%[2]s/*)
      if [ -z "${%[1]s_done:-}" ]; then
        %[1]s_done=1
        [ -f %[3]s ] && . %[3]s
      fi ;;
  esac
}
if [ -n "${ZSH_VERSION:-}" ]; then
  autoload -Uz add-zsh-hook && add-zsh-hook precmd %[1]s
else
  case ";${PROMPT_COMMAND:-};" in *";%[1]s;"*) ;; *) PROMPT_COMMAND="%[1]s${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;; esac
fi
%[1]s
`, fn, q(ws), q(snippet)))
}
