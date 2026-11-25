package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// One sshd per Unix account for everyone who uses Hearth there from a
// workspace (several people sharing root in a GPU container). Each Hearth
// account keeps its block of keys in one shared authorized_keys file;
// every key carries environment="HEARTH_USER=...", environment="HEARTH_HOME=..."
// so a session knows whose agent homes to use (the end of the shared rc
// files reads them), which sshd allows for exactly these two variables
// (PermitUserEnvironment with a pattern list, OpenSSH 7.8). Whichever
// daemon finds it missing starts it; the others publish the same port and
// host key. It runs detached, so it outlives the daemon that started it.

// sharedSSHDFlags versions the options of the shared sshd; a running one
// started with others is restarted (sessions already open stay).
const sharedSSHDFlags = 1

// sharedSSHEnv are the variables a key may set in a session.
const sharedSSHEnv = "HEARTH_USER,HEARTH_HOME"

type sharedSSHDState struct {
	PID       int      `json:"pid,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Port      int      `json:"port,omitempty"`
	Flags     int      `json:"flags,omitempty"`
}

// sharedSSHDir holds the shared sshd's files: inside ~/.ssh, where sshd's
// strict modes accept the authorized_keys file.
func sharedSSHDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(h, ".ssh", "hearth", "shared")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// lockSharedSSHD serialises the daemons of this account's people.
func lockSharedSSHD(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func loadSharedSSHD(dir string) sharedSSHDState {
	var st sharedSSHDState
	_ = loadJSON(filepath.Join(dir, "state.json"), &st)
	return st
}

func saveSharedSSHD(dir string, st sharedSSHDState) error {
	return saveJSON(filepath.Join(dir, "state.json"), st)
}

// sshdUserEnvPatterns: PermitUserEnvironment takes a pattern list since
// OpenSSH 7.8 (before, only yes/no, which would let ~/.ssh/environment
// set anything). The client's version stands for the server's, as in
// sshdSupportsExpiry.
func sshdUserEnvPatterns() bool {
	out, _ := exec.Command("ssh", "-V").CombinedOutput()
	m := sshdVersionRE.FindStringSubmatch(string(out))
	if m == nil {
		return false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	return maj > 7 || (maj == 7 && min >= 8)
}

// sharedKeyLines are the device keys of one person for the shared file:
// each marks the session as theirs. A home path sshd cannot quote is
// left out (the rc block falls back to HEARTH_USER).
func sharedKeyLines(lines []string, email, home string) []string {
	opt := `environment="HEARTH_USER=` + strings.ReplaceAll(email, `"`, "") + `"`
	if !strings.ContainsAny(home, " \t\"'\\,") {
		opt += `,environment="HEARTH_HOME=` + home + `"`
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		// keyLines gives `key` or `expiry-time="..." key`.
		if strings.HasPrefix(l, "expiry-time=") {
			out = append(out, opt+","+l)
		} else {
			out = append(out, opt+" "+l)
		}
	}
	return out
}

// sharedHasKeys reports whether the shared file still holds a person's keys.
func sharedHasKeys(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			return true
		}
	}
	return false
}

// joinSharedSSHD puts this person's keys into the shared file and makes
// sure the shared sshd runs on addrs (and the addresses it listened on
// before). It updates s to point at it and returns its host key ("" when
// it has to be probed).
func (e *Engine) joinSharedSSHD(s *sshState, lines, addrs []string) (string, error) {
	dir, err := sharedSSHDir()
	if err != nil {
		return "", err
	}
	unlock, err := lockSharedSSHD(dir)
	if err != nil {
		return "", err
	}
	defer unlock()
	authPath := filepath.Join(dir, "authorized_keys")
	cur, _ := os.ReadFile(authPath)
	want := sharedKeyLines(lines, e.Cfg.Email, e.Home)
	if out := setManagedKeys(string(cur), e.Cfg.Email, want, nil); out != string(cur) {
		if err := writeFileAtomic(authPath, []byte(out), 0o600); err != nil {
			return "", err
		}
	}
	// This person's own sshd of an older version: the shared one replaces it.
	if !s.External && !s.Joint && alive(s.PID) {
		_ = syscall.Kill(s.PID, syscall.SIGTERM)
		for i := 0; i < 30 && listening(s.Address, s.Port); i++ {
			time.Sleep(100 * time.Millisecond) // its port may be the one to take
		}
		s.PID = 0
	}
	st := loadSharedSSHD(dir)
	all := append([]string{}, addrs...)
	for _, a := range st.Addresses {
		if !slices.Contains(all, a) && localIPv4s()[a] {
			all = append(all, a)
		}
	}
	running := alive(st.PID) && st.Flags == sharedSSHDFlags && st.Port > 0 && len(st.Addresses) > 0 && listening(st.Addresses[0], st.Port)
	covers := running
	for _, a := range addrs {
		covers = covers && slices.Contains(st.Addresses, a)
	}
	hostKey := ""
	if s.Joint && covers && s.Port == st.Port {
		hostKey = s.HostKey
	}
	if !covers {
		if alive(st.PID) {
			_ = syscall.Kill(st.PID, syscall.SIGTERM)
			for i := 0; i < 30 && st.Port > 0 && len(st.Addresses) > 0 && listening(st.Addresses[0], st.Port); i++ {
				time.Sleep(100 * time.Millisecond)
			}
		}
		base := sshPortWanted(e.Home)
		port := 0
		for p := base; p < base+20; p++ {
			if allFree(all, p) {
				port = p
				break
			}
		}
		if port == 0 {
			return "", fmt.Errorf("no free port from %d on %s", base, strings.Join(all, ", "))
		}
		pid, err := startSSHD(dir, authPath, all, port, []string{"-o", "PermitUserEnvironment=" + sharedSSHEnv})
		if err != nil {
			return "", err
		}
		st = sharedSSHDState{PID: pid, Addresses: all, Port: port, Flags: sharedSSHDFlags}
		if err := saveSharedSSHD(dir, st); err != nil {
			return "", err
		}
	}
	// Publish this person's addresses first (their main address), then the
	// others the shared sshd also listens on.
	pub := append([]string{}, addrs...)
	for _, a := range st.Addresses {
		if !slices.Contains(pub, a) {
			pub = append(pub, a)
		}
	}
	s.PID, s.External, s.Shared, s.Joint = 0, false, true, true
	s.Address, s.Addresses, s.Port, s.Flags = pub[0], pub, st.Port, sharedSSHDFlags
	return hostKey, nil
}

// leaveSharedSSHD takes this person's keys out of the shared file and
// stops the shared sshd when nobody's keys are left.
func leaveSharedSSHD(owner string, mine []string) {
	dir, err := sharedSSHDir()
	if err != nil {
		return
	}
	unlock, err := lockSharedSSHD(dir)
	if err != nil {
		return
	}
	defer unlock()
	authPath := filepath.Join(dir, "authorized_keys")
	cur, err := os.ReadFile(authPath)
	if err != nil {
		return
	}
	out := setManagedKeys(string(cur), owner, nil, mine)
	if out != string(cur) {
		_ = writeFileAtomic(authPath, []byte(out), 0o600)
	}
	if !sharedHasKeys(out) {
		if st := loadSharedSSHD(dir); alive(st.PID) {
			_ = syscall.Kill(st.PID, syscall.SIGTERM)
			_ = saveSharedSSHD(dir, sharedSSHDState{})
		}
	}
}

// sharedSSHDRunning reports whether the shared sshd runs (for status).
func sharedSSHDRunning() (sharedSSHDState, bool) {
	dir, err := sharedSSHDir()
	if err != nil {
		return sharedSSHDState{}, false
	}
	st := loadSharedSSHD(dir)
	return st, alive(st.PID) && st.Port > 0 && len(st.Addresses) > 0 && listening(st.Addresses[0], st.Port)
}

// sharedKeysOf counts a person's keys in the shared file.
func sharedKeysOf(owner string) int {
	dir, err := sharedSSHDir()
	if err != nil {
		return 0
	}
	b, err := os.ReadFile(filepath.Join(dir, "authorized_keys"))
	if err != nil {
		return 0
	}
	return len(managedKeys(string(b), owner))
}
