package datatool

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
)

// Rule rewrites a path prefix. {user} in To stands for the account name.
type Rule struct {
	From string
	To   string
}

// ValidUser reports a name that is safe to put into a path ({user}).
func ValidUser(u string) bool {
	if u == "" || len(u) > 64 || strings.HasPrefix(u, ".") {
		return false
	}
	for _, r := range u {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// UserOf is the account name of an email address: the part before the @.
func UserOf(email string) string {
	u, _, _ := strings.Cut(strings.TrimSpace(email), "@")
	return u
}

// MapPath turns a path on this machine into the path the storage side
// uses. The first rule whose From is a whole-segment prefix of the cleaned
// path wins. mapped reports whether a rule applied; without one the cleaned
// path is returned as it is. Paths that are not absolute and not a URL
// (afs://...) are refused: the service cannot see this machine's directories.
func MapPath(rules []Rule, p, user string) (out string, mapped bool, err error) {
	if strings.Contains(p, "://") {
		return p, false, nil
	}
	if !path.IsAbs(p) {
		return "", false, fmt.Errorf("%q is not an absolute path", p)
	}
	// Not resolved silently: "/data/a/../b" is probably a typo, and a copy
	// of another directory than the one meant is hard to undo.
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", false, fmt.Errorf("%q has a .. segment; give the path without it", p)
		}
	}
	clean := path.Clean(p)
	for _, r := range rules {
		from := path.Clean(r.From)
		if from == "." || !path.IsAbs(from) {
			continue
		}
		var rest string
		switch {
		case clean == from:
		case from == "/":
			rest = clean
		case strings.HasPrefix(clean, from+"/"):
			rest = clean[len(from):]
		default:
			continue
		}
		to := r.To
		if strings.Contains(to, "{user}") {
			if !ValidUser(user) {
				return "", false, fmt.Errorf("path rule %q needs {user}, and %q is not a usable account name", r.From, user)
			}
			to = strings.ReplaceAll(to, "{user}", user)
		}
		return path.Clean(to + rest), true, nil
	}
	return clean, false, nil
}

// NeedsAFSUGI reports a task that touches an afs storage.
func NeedsAFSUGI(src, dst string) bool {
	return strings.HasPrefix(src, "afs") || strings.HasPrefix(dst, "afs")
}

// Validate checks a request against what the service reported, so mistakes
// show up before anything is submitted. Limits that are zero (unknown) are
// not enforced here; the service enforces its own.
func Validate(m *Meta, r CreateRequest) error {
	l := m.Limits
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("the task needs a name")
	}
	if l.NameMaxLen > 0 && len([]rune(r.Name)) > l.NameMaxLen {
		return fmt.Errorf("the name is longer than %d characters", l.NameMaxLen)
	}
	if hasControl(r.Name) {
		return errors.New("the name has control characters")
	}
	for _, side := range []struct {
		label string
		sp    StoragePath
	}{{"source", r.Src}, {"destination", r.Dst}} {
		if side.sp.Storage == "" || side.sp.Path == "" {
			return fmt.Errorf("the %s needs a storage and a path", side.label)
		}
		if l.PathMaxLen > 0 && len(side.sp.Path) > l.PathMaxLen {
			return fmt.Errorf("the %s path is longer than %d characters", side.label, l.PathMaxLen)
		}
		if hasControl(side.sp.Path) {
			return fmt.Errorf("the %s path has control characters", side.label)
		}
		for _, seg := range strings.Split(side.sp.Path, "/") {
			if seg == ".." {
				return fmt.Errorf("the %s path has a .. segment", side.label)
			}
		}
	}
	if r.Src.Storage == r.Dst.Storage {
		return fmt.Errorf("source and destination are both on %s; the service copies between two different storages", r.Src.Storage)
	}
	ok := false
	for _, p := range m.ValidPairs {
		if p.Src == r.Src.Storage && p.Dst == r.Dst.Storage {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("the service does not copy from %s to %s (see: hearth transfer meta)", r.Src.Storage, r.Dst.Storage)
	}
	o := r.Options
	if o.Transfers < 0 || o.Checkers < 0 {
		return errors.New("transfers and checkers cannot be negative")
	}
	if l.TransfersMax > 0 && o.Transfers > l.TransfersMax {
		return fmt.Errorf("transfers is at most %d", l.TransfersMax)
	}
	if l.CheckersMax > 0 && o.Checkers > l.CheckersMax {
		return fmt.Errorf("checkers is at most %d", l.CheckersMax)
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
