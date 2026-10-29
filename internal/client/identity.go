package client

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/paths"
)

// LocalIdentity is who this machine says the current user is, derived from
// credentials provisioned outside hearth (a company SSO token, Comate's
// login files, the platform's user name). It only picks the account to log
// in to; the server still asks for proof, so this is not an authentication
// bypass.
type LocalIdentity struct {
	Username string
	Email    string
	Source   string
}

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// DetectIdentity looks for local identity information, newest sources
// first. It returns false when nothing trustworthy is available, or when
// several users share this account and nothing says which one is asking:
// guessing would log someone into another person's hearth account.
func DetectIdentity(home string) (LocalIdentity, bool) {
	return detectIdentity(home, true)
}

// DetectLocalIdentity is DetectIdentity without the existing hearth login:
// used when the caller explicitly wants to switch accounts.
func DetectLocalIdentity(home string) (LocalIdentity, bool) {
	return detectIdentity(home, false)
}

func detectIdentity(home string, useConfig bool) (LocalIdentity, bool) {
	if v := strings.TrimSpace(os.Getenv("HEARTH_IDENTITY")); v != "" {
		if strings.Contains(v, "@") {
			u := v[:strings.Index(v, "@")]
			return LocalIdentity{Username: u, Email: v, Source: "$HEARTH_IDENTITY"}, usernameRE.MatchString(u)
		}
		if usernameRE.MatchString(v) {
			return LocalIdentity{Username: v, Email: withDomain(home, v), Source: "$HEARTH_IDENTITY"}, true
		}
	}
	// An existing login for this workspace is stronger than anything the
	// platform says: several people may share one Unix account.
	if cfg, err := LoadConfig(home); useConfig && err == nil && cfg.Email != "" {
		u, _, _ := strings.Cut(cfg.Email, "@")
		return LocalIdentity{Username: u, Email: cfg.Email, Source: "hearth config"}, true
	}

	cands := identityCandidates(home)
	if len(cands) == 0 {
		return LocalIdentity{}, false
	}
	ws := workspaceName(home)
	var pick *identityCandidate
	for i := range cands {
		if ws != "" && strings.EqualFold(cands[i].username, ws) {
			pick = &cands[i]
			break
		}
	}
	if pick == nil {
		// Environment variables are only hints: on a shared Unix account
		// they may name whoever started the container. Without a per-user
		// artifact (token file, Comate login, hearth config) do not log in.
		var reliable []identityCandidate
		for _, c := range cands {
			if !c.hint {
				reliable = append(reliable, c)
			}
		}
		distinct := map[string]bool{}
		for _, c := range reliable {
			distinct[strings.ToLower(c.username)] = true
		}
		if len(distinct) != 1 {
			return LocalIdentity{}, false // ambiguous: do not guess
		}
		pick = &reliable[0]
	}
	email := withDomain(home, pick.username)
	return LocalIdentity{Username: pick.username, Email: email, Source: pick.source}, true
}

type identityCandidate struct {
	username string
	source   string
	hint     bool // platform environment: only a hint, not proof
}

// IdentityChoices lists every distinct user name this machine knows, per-user
// artifacts (SSO tokens, Comate logins) first, then platform hints, for
// the user to pick from when DetectIdentity cannot decide (several people
// on one Unix account). It only offers accounts; the server still asks for
// proof.
func IdentityChoices(home string) []LocalIdentity {
	cands := identityCandidates(home)
	seen := map[string]bool{}
	var out []LocalIdentity
	for _, hint := range []bool{false, true} {
		for _, c := range cands {
			k := strings.ToLower(c.username)
			if c.hint != hint || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, LocalIdentity{Username: c.username, Email: withDomain(home, c.username), Source: c.source})
		}
	}
	return out
}

// identityCandidates collects every user name this machine knows about:
// platform environment variables, SSO token caches (the site's
// learn.json token_files) and Comate's per-user login files.
func identityCandidates(home string) []identityCandidate {
	var out []identityCandidate
	cfg := learnConfig(home)
	add := func(u, src string, hint bool) {
		u = strings.TrimSpace(u)
		if usernameRE.MatchString(u) {
			out = append(out, identityCandidate{username: u, source: src, hint: hint})
		}
	}
	for _, env := range []string{"COMATE_USERNAME", "SYS_USER_NAME"} {
		add(os.Getenv(env), "$"+env, true)
	}
	for _, g := range cfg.TokenFiles {
		for _, p := range matches(g) {
			if u := jwtUsername(p, cfg.Identity.UsernameClaim); u != "" {
				add(u, "token "+paths.Contract(p), false)
			}
		}
	}
	userHome, _ := os.UserHomeDir()
	if files, _ := filepath.Glob(filepath.Join(userHome, ".comate", "login-user", "*")); files != nil {
		for _, p := range files {
			add(filepath.Base(p), "comate login "+filepath.Base(p), false)
		}
	}
	return out
}

func matches(glob string) []string {
	g := paths.Expand(glob)
	if !strings.ContainsAny(g, "*?[") {
		if _, err := os.Stat(g); err == nil {
			return []string{g}
		}
		return nil
	}
	m, _ := filepath.Glob(g)
	return m
}

// jwtUsername reads a claim from a JWT-shaped token file (no signature
// check: this only selects an account, the server still verifies).
func jwtUsername(path, claim string) string {
	if claim == "" {
		claim = "username"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	// SSO caches are sometimes stored as {"token": "..."}.
	var wrapped struct {
		Token string `json:"token"`
	}
	if json.Unmarshal([]byte(s), &wrapped) == nil && wrapped.Token != "" {
		s = wrapped.Token
	}
	s = strings.TrimPrefix(s, "Bearer ")
	s = strings.TrimPrefix(s, "Bearer-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	v, _ := claims[claim].(string)
	return v
}

// withDomain builds the account email. The domain comes from the site
// config (an organisation setting), else from the user's git email, so the
// open-source code carries no company domain.
func withDomain(home, username string) string {
	if d := strings.TrimPrefix(learnConfig(home).Identity.EmailDomain, "@"); d != "" {
		return username + "@" + d
	}
	// The workspace's existing hearth account knows the right domain even
	// when the caller is switching to another user name; else the login of
	// the default home (only its domain: it may be another person's).
	for _, h := range []string{home, DefaultHome()} {
		if cfg, err := LoadConfig(h); err == nil && strings.Contains(cfg.Email, "@") {
			return username + "@" + domainOf(cfg.Email)
		}
	}
	if e := gitEmail(); strings.Contains(e, "@") {
		return username + "@" + domainOf(e)
	}
	return ""
}

func domainOf(email string) string {
	_, d, _ := strings.Cut(email, "@")
	return d
}

func gitEmail() string {
	// --global: a repository's own user.email often belongs to another
	// identity (e.g. an open-source fork under the same workspace).
	out, err := exec.Command("git", "config", "--global", "--get", "user.email").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// workspaceName is the directory name of the workspace holding home
// (<workspace>/.hearth). It disambiguates several local users on one
// shared Unix account.
func workspaceName(home string) string {
	if ws := paths.Workspace(); ws != "" {
		return filepath.Base(ws)
	}
	if filepath.Base(home) == ".hearth" {
		return filepath.Base(filepath.Dir(home))
	}
	return ""
}
