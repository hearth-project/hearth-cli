package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// SSH into a machine the way it is usually set up by hand on GPU
// containers: the system sshd with /etc/ssh/sshd_config, overridden to
// public keys only, no passwords and the machine's own addresses only
// (sshd -D -e -p 8022 -o ListenAddress=<bond0> ...), detached with setsid,
// and the accepted keys in ~/.ssh/authorized_keys. Hearth only manages a
// marked block of that file (the keys of the account's active devices), so
// keys added by hand stay, and an sshd already listening on the port (one
// started by hand) is used as it is instead of starting a second one.

const defaultSSHPort = 8022

// sshdFlags versions the options startSSHD passes; an sshd Hearth started
// with older ones is restarted (sessions already open stay).
const sshdFlags = 4

type sshState struct {
	Disabled bool   `json:"disabled,omitempty"` // this machine only
	Address  string `json:"address,omitempty"`  // the first of Addresses: what older clients connect to
	// Addresses sshd listens on (empty in states written before: Address).
	Addresses []string `json:"addresses,omitempty"`
	Port      int      `json:"port,omitempty"`
	PID       int      `json:"pid,omitempty"`
	External  bool     `json:"external,omitempty"` // an sshd Hearth did not start (left running when SSH is turned off)
	HostKey   string   `json:"host_key,omitempty"`
	Shared    bool     `json:"shared,omitempty"` // Hearth's own sshd for a workspace on a shared account
	Joint     bool     `json:"joint,omitempty"`  // the account's one shared sshd (sshd_shared.go), not this person's own
	Flags     int      `json:"flags,omitempty"`  // sshdFlags the running sshd was started with
	KeysHash  string   `json:"keys_hash,omitempty"`
	Endpoint  string   `json:"endpoint,omitempty"` // published value, to skip unchanged uploads
	LastErr   string   `json:"last_error,omitempty"`
}

func sshStatePath(home string) string { return filepath.Join(sshDir(home), "state.json") }

func loadSSHState(home string) sshState {
	var s sshState
	_ = loadJSON(sshStatePath(home), &s)
	return s
}

func saveSSHState(home string, s sshState) error {
	if err := os.MkdirAll(sshDir(home), 0o700); err != nil {
		return err
	}
	return saveJSON(sshStatePath(home), s)
}

// SSHOn reports whether this machine accepts SSH: [ssh] enabled in
// config.toml (default: on for Linux machines) and not turned off here.
func SSHOn(home string) bool {
	if loadSSHState(home).Disabled {
		return false
	}
	c, err := UserConfig(home)
	if err == nil && c.SSH != nil && c.SSH.Enabled != nil {
		return *c.SSH.Enabled
	}
	return runtime.GOOS == "linux"
}

// keyExpiry is the expiry-time option for accepted device keys: today
// plus [ssh] key_days (default 7), day granularity so the file changes
// once a day; "" when off or this sshd is older than OpenSSH 8.2 (it
// would reject the option and with it the key).
func keyExpiry(home string, now time.Time) string {
	days := 7
	if c, err := UserConfig(home); err == nil && c.SSH != nil && c.SSH.KeyDays != nil {
		days = *c.SSH.KeyDays
	}
	if days <= 0 || !sshdSupportsExpiry() {
		return ""
	}
	return now.AddDate(0, 0, days).Format("20060102")
}

var sshdVersionRE = regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)`)

// sshdSupportsExpiry: expiry-time came with OpenSSH 8.2.
func sshdSupportsExpiry() bool {
	out, _ := exec.Command("ssh", "-V").CombinedOutput() // sshd has no -V; the client ships with it
	m := sshdVersionRE.FindStringSubmatch(string(out))
	if m == nil {
		return false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	return maj > 8 || (maj == 8 && min >= 2)
}

// keyLines are the authorized_keys lines of the device keys.
func keyLines(keys []SSHKey, expiry string) []string {
	var out []string
	for _, k := range keys {
		if expiry != "" {
			out = append(out, `expiry-time="`+expiry+`" `+k.Key)
		} else {
			out = append(out, k.Key)
		}
	}
	return out
}

// SSHForwardAgent reports whether sessions to the user's machines forward
// this device's ssh-agent ([ssh] forward_agent, default on).
func SSHForwardAgent(home string) bool {
	c, err := UserConfig(home)
	if err == nil && c.SSH != nil && c.SSH.ForwardAgent != nil {
		return *c.SSH.ForwardAgent
	}
	return true
}

// SSHClientConfigOn reports whether this machine keeps Host entries for the
// user's machines in ~/.ssh/config without being asked ([ssh] config;
// default: on a Mac, where the user's apps connect from).
func SSHClientConfigOn(home string) bool {
	c, err := UserConfig(home)
	if err == nil && c.SSH != nil && c.SSH.Config != nil {
		return *c.SSH.Config
	}
	return runtime.GOOS == "darwin"
}

// SetSSHHere turns SSH on or off for this machine only.
func SetSSHHere(home string, on bool) error {
	s := loadSSHState(home)
	s.Disabled = !on
	return saveSSHState(home, s)
}

func sshPortWanted(home string) int {
	if c, err := UserConfig(home); err == nil && c.SSH != nil && c.SSH.Port > 0 {
		return c.SSH.Port
	}
	return defaultSSHPort
}

// SSHAddresses are the IPv4 addresses sshd listens on, the first one is
// the machine's main address: [ssh] address, else bond0 (common on GPU
// clusters), the addresses the machine's host name resolves to, and the
// source addresses of the routes to the Hearth server and to the rest of
// the network. A machine often has several (a container's overlay and
// the host's network, a second NIC): listening on only one of them left
// machines unreachable whose name resolves to another. Only addresses of
// this machine's own interfaces are taken, never loopback or link-local.
func SSHAddresses(home, server string) []string {
	if c, err := UserConfig(home); err == nil && c.SSH != nil && c.SSH.Address != "" {
		return []string{c.SSH.Address}
	}
	return sshCandidates(localIPv4s(), bond0IPv4s(), hostNameIPv4s(), routeSource(serverHost(server)), routeSource("10.255.255.254"))
}

// sshCandidates orders and filters the candidate addresses: each group in
// turn, only addresses of this machine (local), each once.
func sshCandidates(local map[string]bool, groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		for _, ip := range g {
			if ip != "" && local[ip] && !slices.Contains(out, ip) {
				out = append(out, ip)
			}
		}
	}
	return out
}

// localIPv4s are the usable IPv4 addresses of this machine's interfaces.
func localIPv4s() map[string]bool {
	out := map[string]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a.String())
		if err != nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			continue
		}
		out[ip.String()] = true
	}
	return out
}

func bond0IPv4s() []string {
	ifc, err := net.InterfaceByName("bond0")
	if err != nil {
		return nil
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if ip, _, err := net.ParseCIDR(a.String()); err == nil && ip.To4() != nil {
			out = append(out, ip.String())
		}
	}
	return out
}

// hostNameIPv4s: what the machine's host name resolves to, the address
// other machines find it by (2 s at most).
func hostNameIPv4s() []string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", h)
	if err != nil {
		return nil
	}
	var out []string
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

// serverHost is the host of the Hearth server URL ("" if none).
func serverHost(server string) string {
	u, err := url.Parse(server)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// routeSource is this machine's source address towards host: a UDP
// "connection" picks the route, no packet is sent.
func routeSource(host string) []string {
	if host == "" {
		return nil
	}
	c, err := net.DialTimeout("udp4", net.JoinHostPort(host, "9"), 2*time.Second)
	if err != nil {
		return nil
	}
	defer c.Close()
	return []string{c.LocalAddr().(*net.UDPAddr).IP.String()}
}

// addrs: the addresses sshd listens on (a state written before Addresses
// existed has only Address).
func (s sshState) addrs() []string {
	if len(s.Addresses) > 0 {
		return s.Addresses
	}
	if s.Address != "" {
		return []string{s.Address}
	}
	return nil
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return syscall.Kill(pid, 0) == nil && runtime.GOOS != "linux"
	}
	return strings.Contains(string(b), "sshd")
}

func listening(addr string, port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(addr, strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func portFree(addr string, port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func allFree(addrs []string, port int) bool {
	for _, a := range addrs {
		if !portFree(a, port) {
			return false
		}
	}
	return true
}

// SSHStatus describes this machine's sshd.
type SSHStatus struct {
	On       bool
	Running  bool
	External bool // an sshd started outside Hearth (e.g. by hand), used as it is
	Address  string
	Port     int
	PID      int
	Keys     int
	Shared   bool // the account's one sshd, shared with the other people on it
	Err      string
}

// SSHServerStatus reports this machine's sshd without changing it.
func SSHServerStatus(home string) SSHStatus {
	s := loadSSHState(home)
	st := SSHStatus{On: SSHOn(home), Address: s.Address, Port: s.Port, PID: s.PID, External: s.External, Err: s.LastErr}
	if s.Joint {
		sh, ok := sharedSSHDRunning()
		st.Running, st.PID, st.Shared = ok && sh.Port == s.Port, sh.PID, true
		st.Keys = sharedKeysOf(homeOwner(home))
		return st
	}
	if s.Port > 0 {
		st.Running = (s.External || alive(s.PID)) && listening(s.Address, s.Port)
	}
	if p, err := authorizedKeysPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			st.Keys = len(managedKeys(string(b), homeOwner(home)))
		}
	}
	return st
}

// StopSSHD stops the sshd Hearth started (not one started by hand) and
// removes the device keys from authorized_keys.
func StopSSHD(home string) error {
	s := loadSSHState(home)
	if s.Joint {
		leaveSharedSSHD(homeOwner(home), []string{deviceKeyLine(home)})
	} else if !s.External && alive(s.PID) {
		if err := syscall.Kill(s.PID, syscall.SIGTERM); err != nil {
			return err
		}
	}
	if p, err := authorizedKeysPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			owner := homeOwner(home)
			mine := append(managedKeys(string(b), owner), deviceKeyLine(home))
			if out := setManagedKeys(string(b), owner, nil, mine); out != string(b) {
				_ = writeFileAtomic(p, []byte(out), 0o600)
			}
		}
	}
	s.PID, s.External, s.Port, s.Joint = 0, false, 0, false
	return saveSSHState(home, s)
}

// EnsureSSHD keeps this machine's sshd and its keys in line with the
// account (the daemon runs it every loop): puts the active devices' keys
// into authorized_keys, uses the sshd on the port or starts one, and
// publishes where it listens. Off: stops it and withdraws the endpoint.
func (e *Engine) EnsureSSHD() (SSHStatus, error) {
	home := e.Home
	s := loadSSHState(home)
	if !SSHOn(home) {
		if s.Port > 0 || alive(s.PID) {
			_ = StopSSHD(home)
			s = loadSSHState(home)
		}
		if s.Endpoint != "" {
			if err := e.DeleteSecret(sshHostPrefix + e.Cfg.DeviceID); err == nil || strings.Contains(err.Error(), "no secret") {
				s.Endpoint = ""
				_ = saveSSHState(home, s)
			}
		}
		return SSHServerStatus(home), nil
	}
	fail := func(err error) (SSHStatus, error) {
		s.LastErr = err.Error()
		_ = saveSSHState(home, s)
		return SSHServerStatus(home), err
	}
	keys, err := e.SSHKeys()
	if err != nil {
		return fail(err)
	}
	lines := keyLines(keys, keyExpiry(home, time.Now()))
	authPath, err := authorizedKeysPath()
	if err != nil {
		return fail(err)
	}
	cur, _ := os.ReadFile(authPath)
	if out := setManagedKeys(string(cur), e.Cfg.Email, lines, []string{deviceKeyLine(home)}); out != string(cur) {
		if err := writeFileAtomic(authPath, []byte(out), 0o600); err != nil {
			return fail(err)
		}
	}
	addrs := SSHAddresses(home, e.Cfg.Server)
	if len(addrs) == 0 {
		return fail(errors.New("no IPv4 address to listen on; set [ssh] address in config.toml"))
	}
	addr := addrs[0]
	// On an account several people share (a workspace), sessions through
	// this person's own sshd are marked as theirs (SetEnv HEARTH_USER), so
	// the end of the shared rc file restores their agent homes over
	// whatever a colleague's lines exported. An sshd started by hand is
	// not adopted there: it cannot say whose session it is.
	shared := WorkspaceOf(home) != ""
	sshdAuth := authPath
	setEnv := ""
	if shared {
		if sshdAuth, err = privateAuthorizedKeys(home, lines); err != nil {
			return fail(err)
		}
		setEnv = "HEARTH_USER=" + e.Cfg.Email
		if !strings.ContainsAny(home, " \t\"'") {
			setEnv += " HEARTH_HOME=" + home
		}
	}
	hostKey := s.HostKey
	if shared && sshdUserEnvPatterns() {
		// One sshd for everyone on this account who uses a workspace: each
		// person's keys carry their HEARTH_USER / HEARTH_HOME.
		if hostKey, err = e.joinSharedSSHD(&s, lines, addrs); err != nil {
			return fail(err)
		}
	} else {
		if s.Joint {
			// Back to an sshd of its own (no workspace, or an old OpenSSH).
			leaveSharedSSHD(e.Cfg.Email, []string{deviceKeyLine(home)})
			s.Joint, s.Port = false, 0
		}
		want := sshPortWanted(home)
		ours := !s.External && alive(s.PID) && s.Flags == sshdFlags && s.Shared == shared && slices.Equal(s.addrs(), addrs) && s.Port >= want && s.Port < want+20 && listening(addr, s.Port)
		adopted := !shared && s.External && s.Address == addr && s.Port == want && listening(addr, s.Port)
		if !ours && !adopted {
			hostKey = ""
			if alive(s.PID) && !s.External {
				_ = syscall.Kill(s.PID, syscall.SIGTERM)
				for i := 0; i < 30 && listening(s.Address, s.Port); i++ {
					time.Sleep(100 * time.Millisecond) // not taken for an sshd started by hand
				}
			}
			s.PID, s.External, s.Port = 0, false, 0
			if hk, err := probeHostKey(addr, want); err == nil && !shared {
				// An sshd is already there (started by hand): use it.
				s.External, s.Address, s.Addresses, s.Port, hostKey = true, addr, nil, want, hk
			} else {
				port := 0
				for p := want; p < want+20; p++ {
					if allFree(addrs, p) {
						port = p
						break
					}
				}
				if port == 0 {
					return fail(fmt.Errorf("no free port from %d on %s", want, strings.Join(addrs, ", ")))
				}
				var extra []string
				if setEnv != "" {
					extra = []string{"-o", "SetEnv=" + setEnv} // whose sessions these are (shared accounts)
				}
				pid, err := startSSHD(sshDir(home), sshdAuth, addrs, port, extra)
				if err != nil {
					return fail(err)
				}
				s.PID, s.Address, s.Addresses, s.Port, s.Flags, s.Shared = pid, addr, addrs, port, sshdFlags, shared
			}
		}
		if s.External {
			// An sshd started by hand: publish the addresses it answers on.
			s.Addresses = nil
			for _, a := range addrs {
				if a == s.Address || listening(a, s.Port) {
					s.Addresses = append(s.Addresses, a)
				}
			}
		}
	}
	if hostKey == "" {
		if hostKey, err = probeHostKey(s.Address, s.Port); err != nil {
			return fail(fmt.Errorf("sshd at %s:%d: %w", s.Address, s.Port, err))
		}
	}
	s.HostKey = hostKey
	s.LastErr = ""
	u, _ := user.Current()
	name := e.Cfg.DeviceName
	if name == "" {
		name, _ = os.Hostname()
	}
	ep := SSHEndpoint{DeviceID: e.Cfg.DeviceID, Name: name, Address: s.Address, Port: s.Port, User: u.Username, HostKey: hostKey}
	if a := s.addrs(); len(a) > 1 {
		ep.Addresses = a
	}
	v, _ := json.Marshal(ep)
	if string(v) != s.Endpoint {
		ep.Updated = time.Now().Unix()
		pv, _ := json.Marshal(ep)
		if err := e.SetSecret(Secret{Name: sshHostPrefix + e.Cfg.DeviceID, Value: pv}); err != nil {
			return fail(err)
		}
		s.Endpoint = string(v)
	}
	if err := saveSSHState(home, s); err != nil {
		return SSHServerStatus(home), err
	}
	return SSHServerStatus(home), nil
}

// startSSHD runs the system sshd detached, with the flags of the usual
// manual setup: the system config and host keys when running as root,
// else a config of its own with Hearth's host key.
func startSSHD(dir, authPath string, addrs []string, port int, extra []string) (int, error) {
	bin, err := exec.LookPath("sshd")
	if err != nil {
		if _, err2 := os.Stat("/usr/sbin/sshd"); err2 != nil {
			return 0, errNoSSHD
		}
		bin = "/usr/sbin/sshd"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	u, _ := user.Current()
	args := []string{"-D", "-e"}
	sysHostKeys, _ := filepath.Glob("/etc/ssh/ssh_host_*_key")
	if _, err := os.Stat("/etc/ssh/sshd_config"); err == nil && os.Geteuid() == 0 && len(sysHostKeys) > 0 {
		_ = os.MkdirAll("/run/sshd", 0o755) // privilege separation directory
		args = append(args, "-f", "/etc/ssh/sshd_config")
	} else {
		hk := filepath.Join(dir, "ssh_host_ed25519_key")
		if _, err := ensureKey(hk, "hearth-host"); err != nil {
			return 0, err
		}
		args = append(args, "-f", "/dev/null", "-o", "HostKey="+hk, "-o", "Subsystem=sftp internal-sftp")
	}
	args = append(args, "-p", strconv.Itoa(port))
	for _, a := range addrs {
		args = append(args, "-o", "ListenAddress="+a)
	}
	args = append(args, "-o", "PidFile="+filepath.Join(dir, "sshd.pid"),
		"-o", "AuthenticationMethods=publickey", "-o", "PubkeyAuthentication=yes",
		"-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no",
		"-o", "PermitRootLogin=prohibit-password", "-o", "AllowUsers="+u.Username,
		"-o", "AllowAgentForwarding=yes", // the client decides (ForwardAgent)
		"-o", "ClientAliveInterval=60",
		// Some images set LogLevel DEBUG; with -e the session's debug
		// lines would end up in the user's terminal.
		"-o", "LogLevel=INFO")
	args = append(args, extra...)
	if authPath != filepath.Join(u.HomeDir, ".ssh", "authorized_keys") {
		args = append(args, "-o", "AuthorizedKeysFile="+authPath) // $HOME is not the account's home
	}
	logPath := filepath.Join(dir, "sshd.log")
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > 5<<20 {
		_ = os.Remove(logPath)
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer logf.Close()
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = logf, logf, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go cmd.Wait() // reap it if it exits while we run
	for i := 0; i < 30; i++ {
		if listening(addrs[0], port) {
			return pid, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	tail := ""
	if b, err := os.ReadFile(logPath); err == nil {
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		tail = lines[len(lines)-1]
	}
	_ = cmd.Process.Kill()
	return 0, fmt.Errorf("sshd did not start: %s", tail)
}

// probeHostKey connects to an SSH server and returns its host key
// ("ssh-ed25519 AAAA..."), which also tells an sshd from another service.
func probeHostKey(addr string, port int) (string, error) {
	var got string
	cfg := &ssh.ClientConfig{
		User: "hearth-probe",
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			got = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
			return errors.New("probe")
		},
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256},
		Timeout:           2 * time.Second,
	}
	c, err := ssh.Dial("tcp", net.JoinHostPort(addr, strconv.Itoa(port)), cfg)
	if c != nil {
		c.Close()
	}
	if got != "" {
		return got, nil
	}
	if err == nil {
		err = errors.New("no host key")
	}
	return "", err
}

// SetSSHConfig sets [ssh] enabled for every machine of the account.
func SetSSHConfig(home string, on bool) error {
	c, err := UserConfig(home)
	if err != nil {
		return err
	}
	if c.SSH == nil {
		c.SSH = &library.SSHConfig{}
	}
	c.SSH.Enabled = &on
	return SaveUserConfig(home, c)
}

// strictOK reports whether sshd's StrictModes accepts a file at path: every
// directory above it owned by root or this user and not writable by group
// or others.
func strictOK(path string) bool {
	uid := uint32(os.Getuid())
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		fi, err := os.Stat(d)
		if err != nil {
			return false
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && st.Uid != uid {
			return false
		}
		if fi.Mode().Perm()&0o022 != 0 {
			return false
		}
		if d == "/" || d == filepath.Dir(d) {
			return true
		}
	}
}

// authorizedKeysPath is ~/.ssh/authorized_keys, the file sshd reads.
func authorizedKeysPath() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(h, ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	if !strictOK(p) {
		return "", fmt.Errorf("sshd refuses %s: a directory above it is writable by group or others", p)
	}
	return p, nil
}

// Hearth's blocks in authorized_keys. A Unix account several people share
// holds one block per Hearth account, each kept by that person's daemon;
// akBegin/akEnd is the block of older clients, which had no owner.
const (
	akBegin = "# >>> hearth: keys of your Hearth devices (managed; hearth ssh keys) >>>"
	akEnd   = "# <<< hearth <<<"
)

// akMarkers are the lines around owner's block ("" = the old block).
func akMarkers(owner string) (begin, end string) {
	owner = strings.Map(func(r rune) rune {
		if r < ' ' || r == '>' || r == '<' {
			return '_'
		}
		return r
	}, strings.ToLower(strings.TrimSpace(owner)))
	if owner == "" {
		return akBegin, akEnd
	}
	return "# >>> hearth: keys of " + owner + "'s Hearth devices (managed; hearth ssh keys) >>>", "# <<< hearth: " + owner + " <<<"
}

// cutBlock removes the block between begin and end (the lines included)
// and returns the rest and the block's key lines; ok: there was one.
func cutBlock(text, begin, end string) (rest string, keys []string, ok bool) {
	i := strings.Index(text, begin+"\n")
	if i < 0 {
		return text, nil, false
	}
	j := strings.Index(text[i:], end)
	if j < 0 {
		return text, nil, false
	}
	j += i
	for _, l := range strings.Split(text[i+len(begin):j], "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			keys = append(keys, l)
		}
	}
	return text[:i] + strings.TrimPrefix(text[j+len(end):], "\n"), keys, true
}

// keyBlob is the key of an authorized_keys line, without options and
// comment ("" when the line is not a key).
func keyBlob(line string) string {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return ""
	}
	return string(pk.Marshal())
}

// anyKeyOf reports whether a key of lines is one of mine.
func anyKeyOf(lines, mine []string) bool {
	set := map[string]bool{}
	for _, l := range mine {
		if b := keyBlob(l); b != "" {
			set[b] = true
		}
	}
	for _, l := range lines {
		if set[keyBlob(l)] {
			return true
		}
	}
	return false
}

// managedKeys returns the key lines of owner's block.
func managedKeys(text, owner string) []string {
	b, e := akMarkers(owner)
	_, keys, _ := cutBlock(text, b, e)
	return keys
}

// setManagedKeys replaces owner's block in an authorized_keys text with
// keys (none: removes it), leaving the other lines and other people's
// blocks as they are. The old block without an owner is taken out when it
// holds a key of mine (this person's keys, as written by an older client);
// otherwise it is another person's and stays.
func setManagedKeys(text, owner string, keys, mine []string) string {
	b, e := akMarkers(owner)
	text, _, _ = cutBlock(text, b, e)
	if owner != "" {
		if rest, old, ok := cutBlock(text, akBegin, akEnd); ok && anyKeyOf(old, append(append([]string{}, mine...), keys...)) {
			text = rest
		}
	}
	if len(keys) == 0 {
		return text
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + b + "\n" + strings.Join(keys, "\n") + "\n" + e + "\n"
}

// deviceKeyLine is this device's own Hearth public key ("" if none yet).
func deviceKeyLine(home string) string {
	b, err := os.ReadFile(SSHKeyPath(home) + ".pub")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// homeOwner is the account logged in at home ("" if none).
func homeOwner(home string) string {
	cfg, err := LoadConfig(home)
	if err != nil {
		return ""
	}
	return cfg.Email
}

// privateAuthorizedKeys writes the keys of this person's devices to a file
// of their own (~/.ssh/hearth/<id>/authorized_keys, where sshd's strict
// modes accept it), for their own sshd on a shared account.
func privateAuthorizedKeys(home string, keys []string) (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(h, ".ssh", "hearth", sha(home)[:12], "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	body := strings.Join(keys, "\n")
	if body != "" {
		body += "\n"
	}
	if cur, _ := os.ReadFile(p); string(cur) != body {
		if err := writeFileAtomic(p, []byte(body), 0o600); err != nil {
			return "", err
		}
	}
	return p, nil
}
