package client

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
			snip = string(b)
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

// neutralize replaces lines that source a file missing on this machine
// with a no-op (":" keeps if / fi bodies valid).
func neutralize(s string) (string, []string) {
	var missing []string
	lines := strings.Split(s, "\n")
	for i, l := range lines {
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
	if !ShellOn(e.Home) {
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
		st.Files[f.Secret] = shellFileState{Own: sha(f.OwnShell()), Snippet: sha(body), OwnParas: paraHashes(f.OwnShell()), SnippetParas: paraHashes(body)}
		out = append(out, r)
	}
	return out, saveJSON(shellStatePath(e.Home), st)
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
