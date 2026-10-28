package client

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Source is a remote place to find shared skills and agents (design doc
// 8.3). Lookup order is: local library, then sources in the listed order.
type Source struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // team | git
	Team   string `json:"team,omitempty"`
	URL    string `json:"url,omitempty"`
	Ref    string `json:"ref,omitempty"`    // git branch or tag ("" = default branch)
	Subdir string `json:"subdir,omitempty"` // git: directory holding skills/ and agents/ ("" = repo root)
	Trust  string `json:"trust"`            // trusted | untrusted
	// Auto: added because the user belongs to (or can read) this team;
	// removed again when that ends (SyncTeamSources).
	Auto bool `json:"auto,omitempty"`
}

type sourcesFile struct {
	Sources []Source `json:"sources"`
}

// Sources live in the library so they follow the user to every machine,
// like links.json (they hold no credentials).
func sourcesPath(home string) string { return filepath.Join(LibraryDir(home), "sources.json") }

// LoadSources reads the configured sources.
func LoadSources(home string) ([]Source, error) {
	var f sourcesFile
	err := loadJSON(sourcesPath(home), &f)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return f.Sources, err
}

func saveSources(home string, ss []Source) error {
	return saveJSON(sourcesPath(home), sourcesFile{Sources: ss})
}

// AddSource validates and appends a source.
func AddSource(home string, s Source) error {
	if !library.ValidName(s.Name) {
		return fmt.Errorf("invalid source name %q", s.Name)
	}
	switch s.Type {
	case "team":
		if !library.ValidName(s.Team) {
			return fmt.Errorf("invalid team %q", s.Team)
		}
		if s.Trust == "" {
			s.Trust = "trusted"
		}
	case "git":
		if s.URL == "" || strings.HasPrefix(s.URL, "-") || strings.HasPrefix(s.Ref, "-") {
			return fmt.Errorf("invalid git url or ref")
		}
		if strings.Contains(s.Subdir, "..") || filepath.IsAbs(s.Subdir) {
			return fmt.Errorf("invalid subdir %q", s.Subdir)
		}
		if s.Trust == "" {
			s.Trust = "untrusted"
		}
	default:
		return fmt.Errorf("unknown source type %q (want team or git)", s.Type)
	}
	if s.Trust != "trusted" && s.Trust != "untrusted" {
		return fmt.Errorf("trust must be trusted or untrusted")
	}
	ss, err := LoadSources(home)
	if err != nil {
		return err
	}
	for _, x := range ss {
		if x.Name == s.Name {
			return fmt.Errorf("source %q already exists", s.Name)
		}
	}
	return saveSources(home, append(ss, s))
}

// RemoveSource deletes a source by name.
func RemoveSource(home, name string) error {
	ss, err := LoadSources(home)
	if err != nil {
		return err
	}
	out := ss[:0]
	for _, s := range ss {
		if s.Name != name {
			out = append(out, s)
		}
	}
	if len(out) == len(ss) {
		return fmt.Errorf("no source named %q", name)
	}
	return saveSources(home, out)
}

// gitCheckout makes dir a shallow checkout of ref (or a specific commit)
// and returns the commit hash.
func gitCheckout(src Source, dir, commit string) (string, error) {
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		if _, err := run("-C", dir, "init", "-q"); err != nil {
			return "", err
		}
		if _, err := run("-C", dir, "remote", "add", "origin", "--", src.URL); err != nil {
			return "", err
		}
	}
	want := commit
	if want == "" {
		want = src.Ref
	}
	if want == "" {
		want = "HEAD"
	}
	if _, err := run("-C", dir, "fetch", "-q", "--depth", "1", "origin", want); err != nil {
		if commit == "" {
			return "", err
		}
		// Some servers refuse fetching an arbitrary commit; fall back to history.
		if _, err2 := run("-C", dir, "fetch", "-q", "origin"); err2 != nil {
			return "", err
		}
		want = commit
	} else {
		want = "FETCH_HEAD"
	}
	if _, err := run("-C", dir, "-c", "advice.detachedHead=false", "checkout", "-q", "--force", want); err != nil {
		return "", err
	}
	return run("-C", dir, "rev-parse", "HEAD")
}
