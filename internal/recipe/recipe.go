// Package recipe implements declarative restore recipes (design doc 14):
// ordered, idempotent steps that rebuild a machine's environment from the
// synced library and the encrypted secrets.
package recipe

import (
	"errors"
	"fmt"
	"github.com/hearth-project/hearth-cli/internal/paths"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Step is one action. Which fields apply depends on Type.
type Step struct {
	Type string `toml:"type"` // file | env | run | check | capture | manual
	Name string `toml:"name,omitempty"`

	// file
	Secret   string   `toml:"secret,omitempty"`  // content from a secret ...
	Library  string   `toml:"library,omitempty"` // ... or from a file in the library
	To       []string `toml:"to,omitempty"`
	Mode     string   `toml:"mode,omitempty"`   // octal, default 0600 (secret) / 0644 (library)
	Policy   string   `toml:"policy,omitempty"` // if-absent (default) | warn | overwrite
	SharedOK bool     `toml:"shared_ok,omitempty"`

	// env
	Scope []string          `toml:"scope,omitempty"` // directories; empty = every shell
	Set   map[string]string `toml:"set,omitempty"`
	Alias map[string]string `toml:"alias,omitempty"`

	// run, check, capture
	Run         string `toml:"run,omitempty"`
	Unless      string `toml:"unless,omitempty"` // run: skip when this command succeeds
	StdinSecret string `toml:"stdin_secret,omitempty"`
	Expect      string `toml:"expect,omitempty"` // check: regexp the output must match
	Optional    bool   `toml:"optional,omitempty"`
	Every       string `toml:"every,omitempty"` // capture: e.g. "6h" (daemon)

	// manual
	Text     string `toml:"text,omitempty"`
	DoneWhen string `toml:"done_when,omitempty"`
	TTY      bool   `toml:"tty,omitempty"`

	// tool
	Version    string            `toml:"version,omitempty"`
	URL        string            `toml:"url,omitempty"`     // ${ARCH} ${ARCH_NODE} ${VERSION} expanded
	Mirrors    []string          `toml:"mirrors,omitempty"` // tried in order after url (same sha256)
	Proxy      string            `toml:"proxy,omitempty"`   // HTTP proxy retried when direct downloads fail
	SHA256     map[string]string `toml:"sha256,omitempty"`  // per arch (x86_64, aarch64)
	InstallDir string            `toml:"install_dir,omitempty"`
	Strip      int               `toml:"strip,omitempty"` // tar --strip-components
	Bin        string            `toml:"bin,omitempty"`   // file in the archive to install as an executable
	BinDest    string            `toml:"bin_dest,omitempty"`
	NPM        string            `toml:"npm,omitempty"` // package@version
	Registry   string            `toml:"registry,omitempty"`
	Node       string            `toml:"node,omitempty"`    // name of a tool step providing Node
	Wrapper    string            `toml:"wrapper,omitempty"` // write a script pinning that Node
	Check      string            `toml:"check,omitempty"`   // prints the version; default "<name> --version"
	DependsOn  []string          `toml:"depends_on,omitempty"`

	// service
	Start  string `toml:"start,omitempty"`
	Health string `toml:"health,omitempty"`

	// conditions
	// When limits the step to some machines: keys os, arch, gpu, hostname,
	// label, profile; values are comma-separated alternatives, globs, "!x".
	When         map[string]string `toml:"when,omitempty"`
	WhenSecret   string            `toml:"when_secret,omitempty"`
	UnlessSecret string            `toml:"unless_secret,omitempty"`
}

// Recipe is library/recipes/<name>.toml.
type Recipe struct {
	Name        string `toml:"name"`
	Description string `toml:"description,omitempty"`
	// When applies to every step (a step's own keys take precedence), e.g.
	// a recipe only for H800 machines: when = { profile = "h800" }.
	When  map[string]string `toml:"when,omitempty"`
	Steps []Step            `toml:"step"`
}

// WhenKeys are the conditions steps and recipes may use.
var WhenKeys = []string{"os", "arch", "gpu", "hostname", "label", "profile"}

func validWhen(w map[string]string) error {
	for k := range w {
		ok := false
		for _, x := range WhenKeys {
			ok = ok || x == k
		}
		if !ok {
			return fmt.Errorf("unknown when key %q (want %s)", k, strings.Join(WhenKeys, ", "))
		}
	}
	return nil
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Shared files that other users of a shared root account also rely on
// (design doc 14.3). Writing them needs shared_ok = true.
var protected = []string{"~/.ssh/config", "~/.gitconfig", "~/.npmrc", "~/.vimrc", "~/.bashrc", "~/.profile",
	"~/.bash_profile", "~/.zshrc", "~/.ssh/authorized_keys", "/etc/"}

// Parse decodes and validates a recipe.
func Parse(data []byte) (*Recipe, error) {
	var r Recipe
	dec := toml.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, err
	}
	return &r, r.Validate()
}

// Validate checks every step.
func (r *Recipe) Validate() error {
	if !nameRE.MatchString(r.Name) {
		return fmt.Errorf("invalid recipe name %q", r.Name)
	}
	var errs []error
	if err := validWhen(r.When); err != nil {
		errs = append(errs, err)
	}
	for i, s := range r.Steps {
		if err := validWhen(s.When); err != nil {
			errs = append(errs, fmt.Errorf("step %d (%s): %w", i+1, s.Type, err))
		}
		if err := s.validate(); err != nil {
			errs = append(errs, fmt.Errorf("step %d (%s): %w", i+1, s.Type, err))
		}
	}
	return errors.Join(errs...)
}

func (s Step) validate() error {
	need := func(ok bool, msg string) error {
		if !ok {
			return errors.New(msg)
		}
		return nil
	}
	switch s.Type {
	case "file":
		if (s.Secret == "") == (s.Library == "") {
			return errors.New("exactly one of secret or library is required")
		}
		if strings.Contains(s.Library, "..") || filepath.IsAbs(s.Library) {
			return errors.New("library path must be relative to the library")
		}
		if len(s.To) == 0 {
			return errors.New("to is required")
		}
		for _, t := range s.To {
			if !paths.Portable(t) {
				return fmt.Errorf("target %q must be absolute or start with ~/, ${HOME}/ or ${WORKSPACE}/", t)
			}
			if !s.SharedOK && isProtected(t) {
				return fmt.Errorf("%s is a shared file; prefer an env redirect, or set shared_ok = true", t)
			}
		}
		if _, err := s.mode(); err != nil {
			return err
		}
		switch s.Policy {
		case "", "if-absent", "warn", "overwrite":
		default:
			return fmt.Errorf("unknown policy %q", s.Policy)
		}
	case "env":
		if len(s.Set) == 0 && len(s.Alias) == 0 {
			return errors.New("set or alias is required")
		}
		for k := range s.Set {
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) {
				return fmt.Errorf("invalid variable name %q", k)
			}
		}
		for k := range s.Alias {
			if !regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`).MatchString(k) {
				return fmt.Errorf("invalid alias name %q", k)
			}
		}
		for _, d := range s.Scope {
			if !paths.Portable(d) {
				return fmt.Errorf("scope %q must be absolute or start with ~/, ${HOME}/ or ${WORKSPACE}/", d)
			}
		}
	case "run", "check":
		if err := need(s.Run != "", "run is required"); err != nil {
			return err
		}
		if s.Expect != "" {
			if _, err := regexp.Compile(s.Expect); err != nil {
				return fmt.Errorf("expect: %w", err)
			}
		}
	case "capture":
		if s.Run == "" || s.Secret == "" {
			return errors.New("run and secret are required")
		}
	case "manual":
		return need(s.Text != "", "text is required")
	case "tool":
		return s.validateTool()
	case "service":
		if !nameRE.MatchString(s.Name) || s.Start == "" || s.Health == "" {
			return errors.New("name, start and health are required")
		}
	default:
		return fmt.Errorf("unknown step type %q (want tool, file, env, run, service, check, capture, manual)", s.Type)
	}
	return nil
}

func (s Step) mode() (os.FileMode, error) {
	if s.Mode == "" {
		if s.Secret != "" {
			return 0o600, nil
		}
		return 0o644, nil
	}
	m, err := strconv.ParseUint(s.Mode, 8, 32)
	if err != nil || m > 0o777 {
		return 0, fmt.Errorf("invalid mode %q", s.Mode)
	}
	return os.FileMode(m), nil
}

// isProtected reports whether p names a file other users of the account
// share. ${WORKSPACE}/... paths are personal by definition; where a machine
// has no workspace they are checked when the step runs (sharedHere).
func isProtected(p string) bool {
	if strings.HasPrefix(p, "${WORKSPACE}") {
		return false
	}
	for _, x := range protected {
		if p == x || (strings.HasSuffix(x, "/") && strings.HasPrefix(p, x)) || ExpandHome(p) == ExpandHome(x) {
			return true
		}
	}
	return false
}

// sharedHere reports whether a ${WORKSPACE}/... target lands on a shared
// file on this machine, which happens when it has no workspace.
func sharedHere(t string) bool {
	if !strings.HasPrefix(t, "${WORKSPACE}") || paths.Workspace() != "" {
		return false
	}
	p := ExpandHome(t)
	for _, x := range protected {
		if p == ExpandHome(x) || (strings.HasSuffix(x, "/") && strings.HasPrefix(p, x)) {
			return true
		}
	}
	return false
}

// ExpandHome resolves a leading ~/.
func ExpandHome(p string) string { return paths.Expand(p) }

// LoadDir reads every *.toml recipe in dir, sorted by name.
func LoadDir(dir string) ([]*Recipe, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Recipe
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		r, err := Parse(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if r.Name+".toml" != e.Name() {
			return nil, fmt.Errorf("%s: name %q does not match the file name", e.Name(), r.Name)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

var (
	hexRE     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	npmRE     = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*@[0-9A-Za-z.+-]+$`)
	versionRE = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]*$`)
)

func pathOK(p string) bool {
	return p == "" || paths.Portable(p)
}

func (s Step) validateTool() error {
	if !nameRE.MatchString(s.Name) {
		return fmt.Errorf("invalid tool name %q", s.Name)
	}
	if (s.URL == "") == (s.NPM == "") {
		return errors.New("exactly one of url or npm is required")
	}
	if s.Version != "" && !versionRE.MatchString(s.Version) {
		return fmt.Errorf("invalid version %q", s.Version)
	}
	if s.URL != "" {
		if !strings.HasPrefix(s.URL, "https://") && !strings.HasPrefix(s.URL, "http://") {
			return errors.New("url must be http(s)")
		}
		if s.Proxy != "" && !strings.HasPrefix(s.Proxy, "http://") && !strings.HasPrefix(s.Proxy, "https://") {
			return errors.New("proxy must be an http(s) URL")
		}
		for _, m := range s.Mirrors {
			if !strings.HasPrefix(m, "https://") && !strings.HasPrefix(m, "http://") {
				return errors.New("mirrors must be http(s)")
			}
		}
		if len(s.SHA256) == 0 {
			return errors.New("sha256 (per architecture) is required for downloads")
		}
		for a, h := range s.SHA256 {
			if !hexRE.MatchString(h) {
				return fmt.Errorf("sha256 for %s is not 64 lowercase hex characters", a)
			}
		}
		if s.InstallDir == "" && s.Bin == "" && s.BinDest == "" {
			return errors.New("install_dir, bin or bin_dest is required")
		}
	}
	if s.NPM != "" && !npmRE.MatchString(s.NPM) {
		return fmt.Errorf("npm must be name@version, got %q", s.NPM)
	}
	if s.Registry != "" && !strings.HasPrefix(s.Registry, "http") {
		return errors.New("registry must be a URL")
	}
	if strings.Contains(s.Bin, "..") || filepath.IsAbs(s.Bin) {
		return errors.New("bin must be a relative path inside the archive")
	}
	for _, p := range []string{s.InstallDir, s.BinDest, s.Wrapper} {
		if !pathOK(p) {
			return fmt.Errorf("path %q must be absolute or start with ~/", p)
		}
	}
	if s.Wrapper != "" && s.Node == "" {
		return errors.New("wrapper needs node")
	}
	return nil
}
