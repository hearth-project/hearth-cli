package client

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/backup"
	"github.com/hearth-project/hearth-cli/internal/library"
)

// Automatic learning: the daemon (and setup) record what `hearth learn`
// would propose, with the proposal it would make (all machines, or only
// machines like this one when an item looks machine-specific), so a machine
// that disappears without anyone running learn loses nothing.
//
//   - Only new items: a stored copy that differs from this machine
//     ("changed") is left for `hearth learn` to decide, since the other
//     machine's version may be the right one.
//   - Credentials follow the backup selection (@credentials, on by
//     default; "hearth backup disable @credentials" stops them).
//   - Items the user said "never" to, tools already pinned, and env values
//     that look like they carry a password stay out.
//   - config.toml [learn] auto = false turns it off.

// AutoLearnOn reports whether automatic learning is enabled (default on).
func AutoLearnOn(home string) bool {
	c, err := UserConfig(home)
	if err != nil || c.Learn == nil || c.Learn.Auto == nil {
		return true
	}
	return *c.Learn.Auto
}

// AutoCredentials reports [learn] auto_credentials and whether the user
// decided it.
func AutoCredentials(home string) (on, decided bool) {
	c, err := UserConfig(home)
	if err != nil || c.Learn == nil || c.Learn.AutoCredentials == nil {
		return false, false
	}
	return *c.Learn.AutoCredentials, true
}

// SetAutoCredentials records the user's answer (synced in config.toml).
func SetAutoCredentials(home string, on bool) error {
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	if c.Learn == nil {
		c.Learn = &library.LearnConfig{}
	}
	c.Learn.AutoCredentials = &on
	return SaveUserConfig(home, c)
}

// LearnPending lists the findings automatic learning leaves to the user:
// changed copies, and credentials while auto_credentials is off or not
// decided (everything new when automatic learning is off).
func (e *Engine) LearnPending() []Finding {
	learned, err := LoadLearned(e.Home)
	if err != nil {
		return nil
	}
	ignored := Ignored(e.Home)
	pinned := map[string]bool{}
	if m, err := LoadTools(e.Home); err == nil {
		for _, t := range m.Tools {
			pinned[t.Name] = true
		}
	}
	var fs []Finding
	for _, f := range e.Discover() {
		if !ignored[f.ID] {
			fs = append(fs, f)
		}
	}
	auto := AutoLearnOn(e.Home)
	st := e.LearnStatus(fs, learned)
	var out []Finding
	for _, f := range fs {
		switch {
		case st[f.ID] == "known":
		case st[f.ID] == "changed", !auto, !e.autoLearnable(f, pinned):
			if f.Kind == "library" || (f.Kind == "tool" && f.tool != nil && pinned[f.tool.Name]) {
				continue // imported by setup / pinned already
			}
			out = append(out, f)
		}
	}
	return out
}

// AutoLearned is one item AutoLearn recorded (or failed to).
type AutoLearned struct {
	ID, Kind, Title string
	Profile         string // set when recorded for machines like this only
	Detail          string // what was stored, or the error
	Failed          bool
}

var userinfoRE = regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`)

// autoLearnable filters what may be recorded without asking.
func (e *Engine) autoLearnable(f Finding, pinned map[string]bool) bool {
	switch f.Kind {
	case "library":
		return false // AutoImport does this (with a secret scan)
	case "credential", "identity":
		on, _ := AutoCredentials(e.Home)
		return on && BackupSelection(e.Home).Enabled(backup.Credentials)
	case "tool":
		return f.tool == nil || !pinned[f.tool.Name] // never move a pin another machine set
	case "env":
		for _, s := range f.steps {
			for _, v := range s.Set {
				if userinfoRE.MatchString(v) {
					return false // proxy URL with a password: plaintext recipe
				}
			}
		}
	}
	return true
}

// AutoLearn records every new finding that may be recorded without asking,
// saves the learned recipe and pushes the library when something changed.
func (e *Engine) AutoLearn() ([]AutoLearned, error) {
	if !AutoLearnOn(e.Home) {
		return nil, nil
	}
	learned, err := LoadLearned(e.Home)
	if err != nil {
		return nil, err
	}
	ignored := Ignored(e.Home)
	pinned := map[string]bool{}
	if m, err := LoadTools(e.Home); err == nil {
		for _, t := range m.Tools {
			pinned[t.Name] = true
		}
	}
	var found []Finding
	for _, f := range e.Discover() {
		if !ignored[f.ID] && e.autoLearnable(f, pinned) {
			found = append(found, f)
		}
	}
	if len(found) == 0 {
		return nil, nil
	}
	status := e.LearnStatus(found, learned)
	before := StepTOML(learned.Steps)
	profile := ""
	var out []AutoLearned
	for _, f := range found {
		if status[f.ID] != "new" {
			continue
		}
		r := AutoLearned{ID: f.ID, Kind: f.Kind, Title: f.Title}
		var did string
		var err error
		if f.PerMachine {
			if profile == "" {
				if profile, _, err = EnsureHereProfile(e.Home); err != nil {
					return out, err
				}
			}
			r.Profile = profile
			did, err = e.AcceptFor(f, learned, profile)
		} else {
			did, err = e.Accept(f, learned)
		}
		if err != nil {
			r.Failed, r.Detail = true, err.Error()
		} else {
			r.Detail = did
		}
		out = append(out, r)
	}
	if StepTOML(learned.Steps) != before {
		if err := SaveRecipe(e.Home, learned); err != nil {
			return out, err
		}
	}
	if len(out) > 0 {
		if _, err := e.LibPush(false); err != nil {
			return out, fmt.Errorf("library push: %w", err)
		}
	}
	return out, nil
}

// Summary is a short "kind: title" list for logs.
func (a AutoLearned) Summary() string {
	s := a.Kind + " " + strings.TrimSpace(a.Title)
	if a.Profile != "" {
		s += " (machines like this: " + a.Profile + ")"
	}
	return s
}
