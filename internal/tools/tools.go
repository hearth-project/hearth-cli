// Package tools keeps the coding-agent CLIs themselves in sync: a manifest
// in the library (tools.json) pins versions, and `hearth tools install`
// brings a new machine to that state.
package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Tool is one manifest entry. Built-in tools only need Name and Version;
// custom tools also carry shell commands, which run only after the user
// approved them on this machine (the manifest is synced, so it must not be
// able to run arbitrary code on its own).
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"` // "" = any version
	Check   string `json:"check,omitempty"`   // custom: prints the version
	Install string `json:"install,omitempty"` // custom: installs it (may use $VERSION)
}

type builtin struct {
	NPM    string
	Binary string
}

// Builtins are installed with npm at a validated version.
var Builtins = map[string]builtin{
	"codex":  {NPM: "@openai/codex", Binary: "codex"},
	"claude": {NPM: "@anthropic-ai/claude-code", Binary: "claude"},
}

var (
	versionRE = regexp.MustCompile(`\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?`)
	strictRE  = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
	nameRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// Manifest is library/tools.json.
type Manifest struct {
	Tools []Tool `json:"tools"`
}

func Load(path string) (*Manifest, error) {
	m := &Manifest{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, m)
}

func (m *Manifest) Save(path string) error {
	sort.Slice(m.Tools, func(i, j int) bool { return m.Tools[i].Name < m.Tools[j].Name })
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Validate checks a tool entry.
func (t Tool) Validate() error {
	if !nameRE.MatchString(t.Name) {
		return fmt.Errorf("invalid tool name %q", t.Name)
	}
	if t.Version != "" && !strictRE.MatchString(t.Version) {
		return fmt.Errorf("invalid version %q (want x.y.z)", t.Version)
	}
	if _, ok := Builtins[t.Name]; !ok && (t.Check == "" || t.Install == "") {
		return fmt.Errorf("%s is not built in (%s); give --check and --install commands", t.Name, strings.Join(BuiltinNames(), ", "))
	}
	return nil
}

func BuiltinNames() []string {
	var n []string
	for k := range Builtins {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// Set adds or replaces an entry.
func (m *Manifest) Set(t Tool) error {
	if err := t.Validate(); err != nil {
		return err
	}
	for i := range m.Tools {
		if m.Tools[i].Name == t.Name {
			m.Tools[i] = t
			return nil
		}
	}
	m.Tools = append(m.Tools, t)
	return nil
}

// Installed returns the version found on this machine ("" if missing).
func Installed(t Tool) string {
	var out []byte
	var err error
	if b, ok := Builtins[t.Name]; ok && t.Check == "" {
		out, err = exec.Command(b.Binary, "--version").CombinedOutput()
	} else {
		out, err = exec.Command("sh", "-c", t.Check).CombinedOutput()
	}
	if err != nil {
		return ""
	}
	if v := versionRE.FindString(string(out)); v != "" {
		return v
	}
	return "unknown"
}

// fingerprint identifies the exact commands a user approved.
func (t Tool) fingerprint() string {
	h := sha256.Sum256([]byte(t.Check + "\x00" + t.Install))
	return hex.EncodeToString(h[:])
}

// Approvals records custom commands approved on this machine.
type Approvals map[string]string // name -> fingerprint

func LoadApprovals(path string) (Approvals, error) {
	a := Approvals{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	return a, json.Unmarshal(b, &a)
}

func (a Approvals) Save(path string) error {
	b, _ := json.MarshalIndent(a, "", "  ")
	return os.WriteFile(path, b, 0o600)
}

// Status is the result for one tool.
type Status struct {
	Tool      Tool
	Installed string
	Action    string // ok, installed, failed, needs-approval, missing-npm
	Detail    string
}

// Sync installs missing or mismatched tools. Built-ins are installed with
// npm; custom tools only if approved (approve=true records approval).
func Sync(m *Manifest, approvals Approvals, approve, dryRun bool) []Status {
	return SyncWith(m, approvals, SyncOptions{Approve: approve, DryRun: dryRun})
}

// SyncOptions tunes SyncWith.
type SyncOptions struct {
	Approve, DryRun bool
	// MissingOnly leaves an installed tool alone even when its version
	// differs from the manifest (setup: a working CLI is not replaced).
	MissingOnly bool
	// Skip names tools not to touch (agents not restored on this machine).
	Skip map[string]bool
}

// SyncWith is Sync with options.
func SyncWith(m *Manifest, approvals Approvals, o SyncOptions) []Status {
	approve, dryRun := o.Approve, o.DryRun
	var out []Status
	for _, t := range m.Tools {
		if o.Skip[t.Name] {
			continue
		}
		st := Status{Tool: t, Installed: Installed(t)}
		if st.Installed != "" && (t.Version == "" || st.Installed == t.Version) {
			st.Action = "ok"
			out = append(out, st)
			continue
		}
		if st.Installed != "" && o.MissingOnly {
			st.Action, st.Detail = "kept", fmt.Sprintf("another machine has %s; upgrade: hearth tools install", t.Version)
			out = append(out, st)
			continue
		}
		if err := t.Validate(); err != nil {
			st.Action, st.Detail = "failed", err.Error()
			out = append(out, st)
			continue
		}
		var cmd *exec.Cmd
		if b, ok := Builtins[t.Name]; ok && t.Install == "" {
			if _, err := exec.LookPath("npm"); err != nil {
				st.Action, st.Detail = "missing-npm", "install Node.js/npm first"
				out = append(out, st)
				continue
			}
			pkg := b.NPM
			if t.Version != "" {
				pkg += "@" + t.Version
			}
			cmd = exec.Command("npm", "install", "-g", pkg)
			st.Detail = "npm install -g " + pkg
		} else {
			if approvals[t.Name] != t.fingerprint() && !approve {
				st.Action, st.Detail = "needs-approval", t.Install
				out = append(out, st)
				continue
			}
			approvals[t.Name] = t.fingerprint()
			cmd = exec.Command("sh", "-c", t.Install)
			cmd.Env = append(os.Environ(), "VERSION="+t.Version)
			st.Detail = t.Install
		}
		if dryRun {
			st.Action = "would-install"
			out = append(out, st)
			continue
		}
		if b, err := cmd.CombinedOutput(); err != nil {
			// The recorded version may not be in this machine's npm
			// registry (a mirror that lags behind): keep what is installed,
			// or install the newest version the registry has.
			if _, builtin := Builtins[t.Name]; builtin && t.Install == "" && t.Version != "" && npmNoVersion(string(b)) {
				if st.Installed != "" {
					st.Action, st.Detail = "kept", fmt.Sprintf("%s is not in the npm registry here; keeping %s", t.Version, st.Installed)
					out = append(out, st)
					continue
				}
				pkg := Builtins[t.Name].NPM
				b, err = exec.Command("npm", "install", "-g", pkg).CombinedOutput()
				if err == nil {
					st.Installed, st.Action = Installed(t), "installed"
					st.Detail = fmt.Sprintf("%s is not in the npm registry here; installed the newest it has", t.Version)
					out = append(out, st)
					continue
				}
			}
			st.Action, st.Detail = "failed", fmt.Sprintf("%s: %v\n%s", st.Detail, err, tail(string(b), 600))
		} else {
			st.Installed, st.Action = Installed(t), "installed"
		}
		out = append(out, st)
	}
	return out
}

// npmNoVersion recognizes npm's "no matching version" error.
func npmNoVersion(out string) bool {
	return strings.Contains(out, "ETARGET") || strings.Contains(out, "No matching version found")
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
