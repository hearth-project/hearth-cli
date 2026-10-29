package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// SSH into a machine the way it is usually set up by hand on GPU
// containers: the system sshd with /etc/ssh/sshd_config, overridden to
// public keys only, no passwords and the main interface only
// (sshd -D -e -p 8022 -o ListenAddress=<bond0> ...), detached with setsid,
// and the accepted keys in ~/.ssh/authorized_keys. Hearth only manages a
// marked block of that file (the keys of the account's active devices), so
// keys added by hand stay, and an sshd already listening on the port (one
// started by hand) is used as it is instead of starting a second one.

const defaultSSHPort = 8022

// sshdFlags versions the options startSSHD passes; an sshd Hearth started
// with older ones is restarted (sessions already open stay).
const sshdFlags = 2

type sshState struct {
	Disabled bool   `json:"disabled,omitempty"` // this machine only
	Address  string `json:"address,omitempty"`
	Port     int    `json:"port,omitempty"`
	PID      int    `json:"pid,omitempty"`
	External bool   `json:"external,omitempty"` // an sshd Hearth did not start (left running when SSH is turned off)
	HostKey  string `json:"host_key,omitempty"`
	Flags    int    `json:"flags,omitempty"` // sshdFlags the running sshd was started with
	KeysHash string `json:"keys_hash,omitempty"`
	Endpoint string `json:"endpoint,omitempty"` // published value, to skip unchanged uploads
	LastErr  string `json:"last_error,omitempty"`
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

// SSHAddress is the IPv4 address sshd listens on: [ssh] address, else
// bond0 (common on GPU clusters), else the address of the default route.
func SSHAddress(home string) string {
	if c, err := UserConfig(home); err == nil && c.SSH != nil && c.SSH.Address != "" {
		return c.SSH.Address
	}
	if ifc, err := net.InterfaceByName("bond0"); err == nil {
		if addrs, err := ifc.Addrs(); err == nil {
			for _, a := range addrs {
				if ip, _, err := net.ParseCIDR(a.String()); err == nil && ip.To4() != nil {
					return ip.String()
				}
			}
		}
	}
	if c, err := net.Dial("udp4", "10.255.255.254:9"); err == nil { // no packet is sent
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).IP.String()
	}
	return ""
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

// SSHStatus describes this machine's sshd.
type SSHStatus struct {
	On       bool
	Running  bool
	External bool // an sshd started outside Hearth (e.g. by hand), used as it is
	Address  string
	Port     int
	PID      int
	Keys     int
	Err      string
}

// SSHServerStatus reports this machine's sshd without changing it.
func SSHServerStatus(home string) SSHStatus {
	s := loadSSHState(home)
	st := SSHStatus{On: SSHOn(home), Address: s.Address, Port: s.Port, PID: s.PID, External: s.External, Err: s.LastErr}
	if s.Port > 0 {
		st.Running = (s.External || alive(s.PID)) && listening(s.Address, s.Port)
	}
	if p, err := authorizedKeysPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			st.Keys = len(managedKeys(string(b)))
		}
	}
	return st
}

// StopSSHD stops the sshd Hearth started (not one started by hand) and
// removes the device keys from authorized_keys.
func StopSSHD(home string) error {
	s := loadSSHState(home)
	if !s.External && alive(s.PID) {
		if err := syscall.Kill(s.PID, syscall.SIGTERM); err != nil {
			return err
		}
	}
	if p, err := authorizedKeysPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			if out := setManagedKeys(string(b), nil); out != string(b) {
				_ = writeFileAtomic(p, []byte(out), 0o600)
			}
		}
	}
	s.PID, s.External, s.Port = 0, false, 0
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
	var lines []string
	for _, k := range keys {
		lines = append(lines, k.Key)
	}
	authPath, err := authorizedKeysPath()
	if err != nil {
		return fail(err)
	}
	cur, _ := os.ReadFile(authPath)
	if out := setManagedKeys(string(cur), lines); out != string(cur) {
		if err := writeFileAtomic(authPath, []byte(out), 0o600); err != nil {
			return fail(err)
		}
	}
	addr := SSHAddress(home)
	if addr == "" {
		return fail(errors.New("no IPv4 address to listen on; set [ssh] address in config.toml"))
	}
	want := sshPortWanted(home)
	ours := !s.External && alive(s.PID) && s.Flags == sshdFlags && s.Address == addr && s.Port >= want && s.Port < want+20 && listening(addr, s.Port)
	adopted := s.External && s.Address == addr && s.Port == want && listening(addr, s.Port)
	hostKey := s.HostKey
	if !ours && !adopted {
		hostKey = ""
		if alive(s.PID) && !s.External {
			_ = syscall.Kill(s.PID, syscall.SIGTERM)
			for i := 0; i < 30 && listening(s.Address, s.Port); i++ {
				time.Sleep(100 * time.Millisecond) // not taken for an sshd started by hand
			}
		}
		s.PID, s.External, s.Port = 0, false, 0
		if hk, err := probeHostKey(addr, want); err == nil {
			// An sshd is already there (started by hand): use it.
			s.External, s.Address, s.Port, hostKey = true, addr, want, hk
		} else {
			port := 0
			for p := want; p < want+20; p++ {
				if portFree(addr, p) {
					port = p
					break
				}
			}
			if port == 0 {
				return fail(fmt.Errorf("no free port from %d on %s", want, addr))
			}
			pid, err := startSSHD(home, authPath, addr, port)
			if err != nil {
				return fail(err)
			}
			s.PID, s.Address, s.Port, s.Flags = pid, addr, port, sshdFlags
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
func startSSHD(home, authPath, addr string, port int) (int, error) {
	bin, err := exec.LookPath("sshd")
	if err != nil {
		if _, err2 := os.Stat("/usr/sbin/sshd"); err2 != nil {
			return 0, errNoSSHD
		}
		bin = "/usr/sbin/sshd"
	}
	dir := sshDir(home)
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
	args = append(args, "-p", strconv.Itoa(port),
		"-o", "ListenAddress="+addr, "-o", "PidFile="+filepath.Join(dir, "sshd.pid"),
		"-o", "AuthenticationMethods=publickey", "-o", "PubkeyAuthentication=yes",
		"-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no",
		"-o", "PermitRootLogin=prohibit-password", "-o", "AllowUsers="+u.Username,
		"-o", "ClientAliveInterval=60",
		// Some images set LogLevel DEBUG; with -e the session's debug
		// lines would end up in the user's terminal.
		"-o", "LogLevel=INFO")
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
		if listening(addr, port) {
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

const (
	akBegin = "# >>> hearth: keys of your Hearth devices (managed; hearth ssh keys) >>>"
	akEnd   = "# <<< hearth <<<"
)

// managedKeys returns the key lines inside Hearth's block.
func managedKeys(text string) []string {
	i, j := strings.Index(text, akBegin), strings.Index(text, akEnd)
	if i < 0 || j < i {
		return nil
	}
	var out []string
	for _, l := range strings.Split(text[i+len(akBegin):j], "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// setManagedKeys replaces Hearth's block in an authorized_keys text (no
// keys: removes it), leaving the other lines as they are.
func setManagedKeys(text string, keys []string) string {
	i, j := strings.Index(text, akBegin), strings.Index(text, akEnd)
	if i >= 0 && j > i {
		text = text[:i] + strings.TrimPrefix(text[j+len(akEnd):], "\n")
	}
	if len(keys) == 0 {
		return text
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + akBegin + "\n" + strings.Join(keys, "\n") + "\n" + akEnd + "\n"
}
