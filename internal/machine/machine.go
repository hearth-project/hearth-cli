// Package machine describes the machine hearth runs on (OS, CPU
// architecture, GPU model, hostname) and matches it against the user's
// machine profiles (library/machines.toml, synced). Habits that hold
// everywhere live in ordinary recipe steps; what differs per kind of
// machine (an H800 box, a GB200 node, a Mac) is scoped to a profile, and
// each profile can have its own working directory.
package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Facts are detected properties of this machine.
type Facts struct {
	OS       string   `json:"os"`        // linux, darwin
	Arch     string   `json:"arch"`      // x86_64, aarch64
	GPU      string   `json:"gpu"`       // normalised model of the first GPU, e.g. h800, gb200, a30, apple; "" = none (matched as "none")
	GPUName  string   `json:"gpu_name"`  // as reported by the driver
	GPUCount int      `json:"gpu_count"` // number of GPUs
	Hostname string   `json:"hostname"`
	Labels   []string `json:"labels,omitempty"` // from $HEARTH_LABELS (comma separated), e.g. "pdc,cluster-a"
}

// Arch returns uname-style architecture names (x86_64, aarch64).
func Arch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return runtime.GOARCH
}

// Detector runs commands; replaced in tests.
var Detector = func(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// gpuModelRE picks the model token out of names such as "NVIDIA H800",
// "NVIDIA H100 80GB HBM3", "NVIDIA GB200", "NVIDIA B200", "NVIDIA A30".
var gpuModelRE = regexp.MustCompile(`(?i)\b(gb\d{3}|gh\d{3}|[abhlv]\d{2,4}[a-z]?|rtx ?\d{4}|t4)\b`)

// NormalizeGPU turns a driver name into a short lowercase model.
func NormalizeGPU(name string) string {
	if m := gpuModelRE.FindString(name); m != "" {
		return strings.ReplaceAll(strings.ToLower(m), " ", "")
	}
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimPrefix(n, "nvidia ")
	return strings.Join(strings.Fields(n), "-")
}

// Detect inspects this machine. It never fails: unknown facts stay empty.
// $HEARTH_MACHINE (JSON, e.g. {"gpu":"h800"}) overrides detected facts,
// for testing recipes of other kinds of machines.
func Detect() Facts {
	f := detect()
	if v := os.Getenv("HEARTH_MACHINE"); v != "" {
		var o Facts
		if json.Unmarshal([]byte(v), &o) == nil {
			if o.OS != "" {
				f.OS = o.OS
			}
			if o.Arch != "" {
				f.Arch = alias("arch", o.Arch)
			}
			if o.GPU != "" {
				f.GPU, f.GPUName, f.GPUCount = o.GPU, o.GPU, max(o.GPUCount, 1)
				if f.GPU == "none" {
					f.GPU, f.GPUName, f.GPUCount = "", "", 0
				}
			}
			if o.Hostname != "" {
				f.Hostname = o.Hostname
			}
			if o.Labels != nil {
				f.Labels = o.Labels
			}
		}
	}
	return f
}

func detect() Facts {
	f := Facts{OS: runtime.GOOS, Arch: Arch()}
	f.Hostname, _ = os.Hostname()
	if out, err := Detector("nvidia-smi", "--query-gpu=name", "--format=csv,noheader"); err == nil {
		var names []string
		for _, l := range strings.Split(string(out), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				names = append(names, l)
			}
		}
		if len(names) > 0 {
			f.GPUName, f.GPUCount, f.GPU = names[0], len(names), NormalizeGPU(names[0])
		}
	}
	if f.GPU == "" && f.OS == "darwin" && f.Arch == "aarch64" {
		f.GPU, f.GPUName, f.GPUCount = "apple", "Apple silicon", 1
	}
	for _, l := range strings.Split(os.Getenv("HEARTH_LABELS"), ",") {
		if l = strings.TrimSpace(l); l != "" {
			f.Labels = append(f.Labels, l)
		}
	}
	return f
}

// Profile is one kind of machine.
type Profile struct {
	Name        string `toml:"name"`
	Description string `toml:"description,omitempty"`
	// Match: every key must match (os, arch, gpu, hostname, label). Values
	// are comma-separated alternatives; shell globs allowed; "!x" negates.
	Match map[string]string `toml:"match,omitempty"`
	// Workspace is where this kind of machine keeps the user's work; it
	// becomes ${WORKSPACE} in recipes when no workspace was set otherwise.
	Workspace string `toml:"workspace,omitempty"`
}

// File is library/machines.toml.
type File struct {
	Profiles []Profile `toml:"profile"`
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// MatchKeys are the facts a profile or a step condition may test.
var MatchKeys = []string{"os", "arch", "gpu", "hostname", "label"}

func validKey(k string) bool {
	for _, x := range MatchKeys {
		if x == k {
			return true
		}
	}
	return false
}

// Validate checks names, keys and patterns.
func (f *File) Validate() error {
	seen := map[string]bool{}
	var errs []error
	for _, p := range f.Profiles {
		if !nameRE.MatchString(p.Name) {
			errs = append(errs, fmt.Errorf("invalid profile name %q", p.Name))
		}
		if seen[p.Name] {
			errs = append(errs, fmt.Errorf("duplicate profile %q", p.Name))
		}
		seen[p.Name] = true
		for k, v := range p.Match {
			if !validKey(k) {
				errs = append(errs, fmt.Errorf("profile %s: unknown match key %q (want %s)", p.Name, k, strings.Join(MatchKeys, ", ")))
			}
			for _, alt := range strings.Split(v, ",") {
				if _, err := filepath.Match(strings.TrimPrefix(strings.TrimSpace(alt), "!"), ""); err != nil {
					errs = append(errs, fmt.Errorf("profile %s: bad pattern %q", p.Name, alt))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// Load reads a profiles file (missing = empty).
func Load(path string) (*File, error) {
	f := &File{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	dec := toml.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, f.Validate()
}

// Save writes the file.
func (f *File) Save(path string) error {
	if err := f.Validate(); err != nil {
		return err
	}
	b, err := toml.Marshal(f)
	if err != nil {
		return err
	}
	head := "# Kinds of machines (hearth machine). Recipe steps can be limited to them with\n# when = { profile = \"name\" }; workspace sets ${WORKSPACE} on matching machines.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(head), b...), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Get returns a profile by name.
func (f *File) Get(name string) (Profile, bool) {
	for _, p := range f.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// Set adds or replaces a profile.
func (f *File) Set(p Profile) {
	for i := range f.Profiles {
		if f.Profiles[i].Name == p.Name {
			f.Profiles[i] = p
			return
		}
	}
	f.Profiles = append(f.Profiles, p)
}

// Remove deletes a profile.
func (f *File) Remove(name string) bool {
	for i, p := range f.Profiles {
		if p.Name == name {
			f.Profiles = append(f.Profiles[:i], f.Profiles[i+1:]...)
			return true
		}
	}
	return false
}

// value returns the facts tested by key ("label" is a list).
func (f Facts) values(key string) []string {
	switch key {
	case "os":
		return []string{f.OS}
	case "arch":
		return []string{f.Arch}
	case "gpu":
		if f.GPU == "" {
			return []string{"none"}
		}
		return []string{f.GPU}
	case "hostname":
		return []string{f.Hostname}
	case "label":
		return f.Labels
	}
	return nil
}

// MatchValue tests one condition such as "h800,h100", "!darwin" or "gpu-*".
// A condition with only negations matches when none of them match.
func (f Facts) MatchValue(key, cond string) bool {
	vals := f.values(key)
	pos, anyPos := false, false
	for _, alt := range strings.Split(cond, ",") {
		alt = strings.ToLower(strings.TrimSpace(alt))
		neg := strings.HasPrefix(alt, "!")
		alt = alias(key, strings.TrimPrefix(alt, "!"))
		hit := false
		for _, v := range vals {
			if ok, _ := filepath.Match(alt, strings.ToLower(v)); ok {
				hit = true
			}
		}
		if neg && hit {
			return false
		}
		if !neg {
			anyPos = true
			pos = pos || hit
		}
	}
	return !anyPos || pos
}

// alias accepts the common other spellings of OS and architecture names.
func alias(key, v string) string {
	switch key + "=" + v {
	case "arch=amd64", "arch=x64":
		return "x86_64"
	case "arch=arm64":
		return "aarch64"
	case "os=mac", "os=macos", "os=osx":
		return "darwin"
	}
	return v
}

// Matches reports whether every condition of p holds. A profile without
// conditions never matches automatically (use `hearth machine use`).
func (f Facts) Matches(p Profile) bool {
	if len(p.Match) == 0 {
		return false
	}
	for k, v := range p.Match {
		if !f.MatchValue(k, v) {
			return false
		}
	}
	return true
}

// Active returns the profiles that apply to this machine: the ones chosen
// explicitly first, then every matching one, in file order.
func (f *File) Active(facts Facts, chosen []string) []Profile {
	var out []Profile
	seen := map[string]bool{}
	for _, n := range chosen {
		if p, ok := f.Get(n); ok && !seen[n] {
			out, seen[n] = append(out, p), true
		}
	}
	for _, p := range f.Profiles {
		if !seen[p.Name] && facts.Matches(p) {
			out, seen[p.Name] = append(out, p), true
		}
	}
	return out
}

// Names returns profile names.
func Names(ps []Profile) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

// Suggest proposes a profile for this machine: by GPU model when there is
// one ("h800"), else by platform ("mac", "linux-aarch64").
func Suggest(f Facts) Profile {
	switch {
	case f.GPU != "" && f.GPU != "apple":
		return Profile{Name: sanitize(f.GPU), Description: fmt.Sprintf("%s machines (%d x %s, %s)", f.GPU, f.GPUCount, f.GPUName, f.Arch),
			Match: map[string]string{"gpu": f.GPU}}
	case f.OS == "darwin":
		return Profile{Name: "mac", Description: "macOS machines", Match: map[string]string{"os": "darwin"}}
	}
	return Profile{Name: sanitize(f.OS + "-" + f.Arch), Description: f.OS + " " + f.Arch + " machines without a GPU",
		Match: map[string]string{"os": f.OS, "arch": f.Arch, "gpu": "none"}}
}

func sanitize(s string) string {
	s = strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(s, "-"))
	return strings.Trim(s, "-._")
}

// Summary is a one-line description of the facts.
func (f Facts) Summary() string {
	gpu := "no GPU"
	if f.GPU != "" {
		gpu = fmt.Sprintf("%d x %s (%s)", f.GPUCount, f.GPUName, f.GPU)
	}
	s := fmt.Sprintf("%s %s, %s, host %s", f.OS, f.Arch, gpu, f.Hostname)
	if len(f.Labels) > 0 {
		l := append([]string(nil), f.Labels...)
		sort.Strings(l)
		s += ", labels " + strings.Join(l, ",")
	}
	return s
}
