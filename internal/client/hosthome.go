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
