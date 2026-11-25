package client

import (
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Disks several machines mount (cluster storage): a workspace there is the
// same directory on every machine of the cluster, but a Hearth home is one
// machine's (its device login, daemon, sshd, agent homes). <ws>/.hearth
// belongs to the machine that logged in there first; every other machine
// keeps its home in <ws>/.hearth/hosts/<host>.

const hostMarker = "host"

// hostKey names this machine: its hostname, lower case, safe as a dir name.
func hostKey() string {
	h, _ := os.Hostname()
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" {
		return "localhost"
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, h)
}

// WorkspaceHome is this machine's Hearth home in workspace ws.
func WorkspaceHome(ws string) string {
	base := filepath.Join(ws, ".hearth")
	if homeOfThisHost(base) {
		return base
	}
	return filepath.Join(base, "hosts", hostKey())
}

// homeOfThisHost reports whether home (a workspace's .hearth) is this
// machine's: it says so (host marker), or nobody is logged in there, or
// its login is a device of this machine.
func homeOfThisHost(home string) bool {
	if b, err := os.ReadFile(filepath.Join(home, hostMarker)); err == nil {
		return strings.TrimSpace(string(b)) == hostKey()
	}
	cfg, err := LoadConfig(home)
	if err != nil {
		return true // nobody logged in there: free for this machine
	}
	return deviceIsThisHost(cfg.DeviceName) || sshdIsLocal(home)
}

// deviceIsThisHost: devices are named after the hostname unless the user
// chose a name (setup --device); an address of this machine counts too.
func deviceIsThisHost(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	h := hostKey()
	if n == "" || n == h || firstLabel(n) == firstLabel(h) {
		return true
	}
	if ip := net.ParseIP(n); ip != nil {
		return isLocalIP(ip)
	}
	return false
}

func firstLabel(h string) string {
	if net.ParseIP(h) != nil {
		return h
	}
	l, _, _ := strings.Cut(h, ".")
	return l
}

func isLocalIP(ip net.IP) bool {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// sshdIsLocal: the home's sshd listens on an address of this machine (a
// device the user named themselves).
func sshdIsLocal(home string) bool {
	ip := net.ParseIP(loadSSHState(home).Address)
	return ip != nil && isLocalIP(ip)
}

// claimHost records that home is this machine's (after a login there).
func claimHost(home string) {
	if WorkspaceOf(home) == "" {
		return
	}
	p := filepath.Join(home, hostMarker)
	if _, err := os.Stat(p); err == nil {
		return
	}
	_ = writeFileAtomic(p, []byte(hostKey()+"\n"), 0o600)
}

// OtherHostHome reports a workspace .hearth that another machine logged in
// to (shared disk), with that device's name.
func OtherHostHome(ws string) (string, bool) {
	base := filepath.Join(ws, ".hearth")
	if homeOfThisHost(base) {
		return "", false
	}
	if cfg, err := LoadConfig(base); err == nil {
		return cfg.DeviceName, true
	}
	return "", true
}

// Several people in one workspace (a directory everyone on a shared Unix
// account uses): <ws>/.hearth and its hosts/<host> belong to the person
// who logged in there first; every other person's homes are under
// <ws>/.hearth/users/<email>, in the same layout (the first machine of that
// person there, then hosts/<host>). Nothing in the directory says who is
// asking, so the home is HEARTH_USER's when it is set (Hearth's sshd marks
// each person's sessions with it), else the first person's.

// workspaceUserKey is a person's directory name under users/.
func workspaceUserKey(email string) string {
	k := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' || r == '@' || r == '+' {
			return r
		}
		return '_'
	}, strings.ToLower(strings.TrimSpace(email)))
	if k == "" || k == "." || k == ".." {
		return "_"
	}
	return k
}

// homeIn is this machine's home in a person's base directory (<ws>/.hearth
// or <ws>/.hearth/users/<email>).
func homeIn(base string) string {
	if homeOfThisHost(base) {
		return base
	}
	return filepath.Join(base, "hosts", hostKey())
}

// WorkspaceHomes are the logged-in homes of this machine in ws, one per
// person, the first person's first.
func WorkspaceHomes(ws string) []string {
	base := filepath.Join(ws, ".hearth")
	bases := []string{base}
	users, _ := os.ReadDir(filepath.Join(base, "users"))
	for _, u := range users {
		if u.IsDir() {
			bases = append(bases, filepath.Join(base, "users", u.Name()))
		}
	}
	var out []string
	for _, b := range bases {
		if h := homeIn(b); fileExists(filepath.Join(h, "config.json")) {
			out = append(out, h)
		}
	}
	return out
}

// WorkspaceHomeFor is email's home in ws on this machine: the one logged in
// as email, else where a new login of email goes (the first person's place
// when nobody else is logged in there).
func WorkspaceHomeFor(ws, email string) string {
	for _, h := range WorkspaceHomes(ws) {
		if cfg, err := LoadConfig(h); err == nil && strings.EqualFold(cfg.Email, email) {
			return h
		}
	}
	if h := WorkspaceHome(ws); !fileExists(filepath.Join(h, "config.json")) {
		return h
	}
	return homeIn(filepath.Join(ws, ".hearth", "users", workspaceUserKey(email)))
}

// WorkspaceHomeHere is the home in ws of whoever asks: HEARTH_USER's when
// it is set, else the first person's.
func WorkspaceHomeHere(ws string) string {
	if u := strings.TrimSpace(os.Getenv("HEARTH_USER")); u != "" {
		return WorkspaceHomeFor(ws, u)
	}
	return WorkspaceHome(ws)
}

// InWorkspaceState reports whether home is one of the homes kept in ws
// (anyone's, any machine's).
func InWorkspaceState(ws, home string) bool {
	base := filepath.Clean(filepath.Join(ws, ".hearth"))
	h := filepath.Clean(home)
	return h == base || strings.HasPrefix(h, base+string(filepath.Separator))
}

// WorkspacePeople are the accounts logged in to ws on this machine.
func WorkspacePeople(ws string) []string {
	var out []string
	for _, h := range WorkspaceHomes(ws) {
		if cfg, err := LoadConfig(h); err == nil {
			out = append(out, cfg.Email)
		}
	}
	return out
}
