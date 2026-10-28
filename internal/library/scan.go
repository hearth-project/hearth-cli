package library

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Finding is a likely secret found before upload.
type Finding struct {
	File string
	Line int
	Rule string
}

func (f Finding) String() string { return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Rule) }

var secretRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"private key", regexp.MustCompile(`-----BEGIN ([A-Z]+ )?PRIVATE KEY-----`)},
	{"AWS access key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\b(ghp|gho|ghs|ghu)_[A-Za-z0-9]{36}\b|\bgithub_pat_[A-Za-z0-9_]{40,}`)},
	{"OpenAI-style API key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{32,}`)},
	{"Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"bearer token", regexp.MustCompile(`(?i)\bauthorization:\s*bearer\s+[A-Za-z0-9._~+/=-]{20,}`)},
	// key = value with a concrete-looking value (not a placeholder).
	{"credential assignment", regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|access[_-]?key)\b["']?\s*[:=]\s*["']?([A-Za-z0-9+/_.=-]{16,})`)},
}

// ScanSecrets looks for likely credentials in text files under root. A line
// containing "hearth:allow-secret" is ignored (for documented examples).
func ScanSecrets(root string) ([]Finding, error) {
	var out []Finding
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.IndexByte(b, 0) >= 0 {
			return nil // binary
		}
		rel, _ := filepath.Rel(root, p)
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		for n := 1; sc.Scan(); n++ {
			// ${secret:NAME} references are the intended way to mention a secret.
			line := secretRef.ReplaceAllString(sc.Text(), "${}")
			if strings.Contains(line, "hearth:allow-secret") || recipeRef.MatchString(line) {
				continue
			}
			for _, r := range secretRules {
				idx := r.re.FindStringSubmatchIndex(line)
				if idx == nil {
					continue
				}
				vs, ve := idx[len(idx)-2], idx[len(idx)-1]
				if placeholder(line[vs:ve]) || codeExpr(line, ve) {
					continue
				}
				out = append(out, Finding{File: rel, Line: n, Rule: r.name})
				break
			}
		}
		return nil
	})
	return out, err
}

// codeExpr reports whether the matched value is followed by a call or
// attribute access, i.e. `token = get_token(...)` rather than a literal.
func codeExpr(line string, end int) bool {
	rest := strings.TrimLeft(line[end:], " ")
	return strings.HasPrefix(rest, "(") || strings.HasPrefix(rest, "[")
}

// Recipe keys that name a secret rather than contain one.
var recipeRef = regexp.MustCompile(`^\s*(secret|stdin_secret|when_secret|unless_secret)\s*=\s*["'][A-Za-z0-9][A-Za-z0-9._-]*["']\s*(#.*)?$`)

var secretRef = regexp.MustCompile(`\$\{secret:[^}]*\}`)

// placeholder filters obvious non-secrets such as env var references.
func placeholder(v string) bool {
	l := strings.ToLower(v)
	for _, s := range []string{"your", "example", "xxxx", "placeholder", "changeme", "redacted", "<", "${", "$("} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}
