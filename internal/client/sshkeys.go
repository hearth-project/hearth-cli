package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSH between the user's machines, authenticated by Hearth: every device
// has its own SSH key (private part never leaves it), the public keys of
// the account's active devices are kept as end-to-end encrypted secrets,
// and each machine with `hearth ssh` on runs an sshd that accepts exactly
// those keys. Revoking a device (hearth device revoke) drops its key from
// every machine within a minute. Machines publish where they can be
// reached (and their host key, so clients verify it) the same way.

const (
	sshKeyPrefix  = "hearth.ssh-key."  // + device id, or "x-<hash>" for keys added by hand
	sshHostPrefix = "hearth.ssh-host." // + device id
)

func sshDir(home string) string { return filepath.Join(home, "ssh") }

// SSHKeyPath is this device's private key.
func SSHKeyPath(home string) string { return filepath.Join(sshDir(home), "id_ed25519") }

// SSHKnownHosts is the known_hosts file Hearth writes from the published
// host keys.
func SSHKnownHosts(home string) string { return filepath.Join(sshDir(home), "known_hosts") }

// ensureKey creates an ed25519 key pair at path (OpenSSH format) unless it
// exists, and returns the public key line.
func ensureKey(path, comment string) (string, error) {
	if b, err := os.ReadFile(path + ".pub"); err == nil {
		if _, err := os.Stat(path); err == nil {
			return strings.TrimSpace(string(b)), nil
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	blk, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, pem.EncodeToMemory(blk), 0o600); err != nil {
		return "", err
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " " + comment
	return line, writeFileAtomic(path+".pub", []byte(line+"\n"), 0o644)
}

// SSHKey is one authorised public key.
type SSHKey struct {
	ID     string `json:"id"` // device id, or x-<hash>
	Name   string `json:"name"`
	Key    string `json:"key"` // authorized_keys line
	Added  int64  `json:"added"`
	Device bool   `json:"device"` // a key of a Hearth device (follows the device)
	// DeviceID is the device a key belongs to: its own Hearth key (ID is
	// the device id) or one of its ~/.ssh/id_*.pub keys.
	DeviceID string `json:"device_id,omitempty"`
}

func (k SSHKey) owner() string {
	if k.DeviceID != "" {
		return k.DeviceID
	}
	return k.ID
}

// SSHEndpoint is where a machine accepts SSH.
type SSHEndpoint struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	HostKey  string `json:"host_key"`
	Updated  int64  `json:"updated"`
	Online   bool   `json:"-"`
}

// PublishDeviceKey makes this device's keys known to the user's machines:
// its own Hearth key and, unless [ssh] share_keys = false, the user's
// usual keys (~/.ssh/id_ed25519.pub, id_ecdsa.pub, id_rsa.pub), so plain
// ssh, scp and IDEs work from this device with no extra setup.
func (e *Engine) PublishDeviceKey() (string, error) {
	host, _ := os.Hostname()
	line, err := ensureKey(SSHKeyPath(e.Home), "hearth:"+host)
	if err != nil {
		return "", err
	}
	id := e.Cfg.DeviceID
	want := map[string]SSHKey{sshKeyPrefix + id: {ID: id, Name: e.Cfg.DeviceName, Key: line, Device: true, DeviceID: id}}
	if shareUserKeys(e.Home) {
		h, _ := os.UserHomeDir()
		for _, f := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
			b, err := os.ReadFile(filepath.Join(h, ".ssh", f))
			if err != nil {
				continue
			}
			pk, _, _, _, err := ssh.ParseAuthorizedKey(b)
			if err != nil {
				continue
			}
			sum := sha256.Sum256(pk.Marshal())
			kid := id + ".u" + hex.EncodeToString(sum[:4])
			want[sshKeyPrefix+kid] = SSHKey{ID: kid, Name: e.Cfg.DeviceName + " ~/.ssh/" + f, Device: true, DeviceID: id,
				Key: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk))) + " " + strings.TrimSuffix(f, ".pub") + "@" + host}
		}
	}
	all, err := e.Secrets()
	if err != nil {
		return "", err
	}
	have := map[string]SSHKey{}
	for _, s := range all {
		if s.Name == sshKeyPrefix+id || strings.HasPrefix(s.Name, sshKeyPrefix+id+".u") {
			var k SSHKey
			_ = json.Unmarshal(s.Value, &k)
			have[s.Name] = k
		}
	}
	for name, k := range want {
		if old, ok := have[name]; ok && old.Key == k.Key && old.Name == k.Name && old.DeviceID == k.DeviceID {
			continue
		}
		k.Added = time.Now().Unix()
		v, _ := json.Marshal(k)
		if err := e.SetSecret(Secret{Name: name, Value: v}); err != nil {
			return "", err
		}
	}
	for name := range have {
		if _, ok := want[name]; !ok {
			_ = e.DeleteSecret(name) // a key removed from ~/.ssh, or share_keys turned off
		}
	}
	return line, nil
}

func shareUserKeys(home string) bool {
	c, err := UserConfig(home)
	if err == nil && c.SSH != nil && c.SSH.ShareKeys != nil {
		return *c.SSH.ShareKeys
	}
	return true
}

// AuthorizeKey adds a public key that is not a Hearth device (e.g. an
// existing laptop key) to every machine.
func (e *Engine) AuthorizeKey(line, name string) (SSHKey, error) {
	line = strings.TrimSpace(line)
	pk, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return SSHKey{}, fmt.Errorf("not an SSH public key: %w", err)
	}
	if name == "" {
		name = comment
	}
	h := sha256.Sum256(pk.Marshal())
	k := SSHKey{ID: "x-" + hex.EncodeToString(h[:6]), Name: name, Key: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk))) + " " + strings.ReplaceAll(name, " ", "_"),
		Added: time.Now().Unix()}
	v, _ := json.Marshal(k)
	return k, e.SetSecret(Secret{Name: sshKeyPrefix + k.ID, Value: v})
}

// UnauthorizeKey removes a key added by hand (by id or name).
func (e *Engine) UnauthorizeKey(ref string) error {
	keys, err := e.SSHKeys()
	if err != nil {
		return err
	}
	for _, k := range keys {
		if !k.Device && (k.ID == ref || k.Name == ref) {
			return e.DeleteSecret(sshKeyPrefix + k.ID)
		}
	}
	return fmt.Errorf("no key %q added by hand (device keys go with their device: hearth device revoke)", ref)
}

// SSHKeys lists the keys allowed to log in: the active devices' own keys
// and keys added by hand. Keys of revoked devices are left out.
func (e *Engine) SSHKeys() ([]SSHKey, error) {
	all, err := e.Secrets()
	if err != nil {
		return nil, err
	}
	devs, err := e.API.Devices()
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for _, d := range devs {
		if d.Status == "" || d.Status == "active" {
			active[d.ID] = true
		}
	}
	var out []SSHKey
	for _, s := range all {
		if !strings.HasPrefix(s.Name, sshKeyPrefix) {
			continue
		}
		var k SSHKey
		if json.Unmarshal(s.Value, &k) != nil || k.Key == "" {
			continue
		}
		if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Key)); err != nil {
			continue
		}
		if k.Device && !active[k.owner()] {
			continue
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SSHEndpoints lists the machines that accept SSH, online ones first.
func (e *Engine) SSHEndpoints() ([]SSHEndpoint, error) {
	all, err := e.Secrets()
	if err != nil {
		return nil, err
	}
	devs, err := e.API.Devices()
	if err != nil {
		return nil, err
	}
	state := map[string]bool{}
	online := map[string]bool{}
	for _, d := range devs {
		state[d.ID] = d.Status == "" || d.Status == "active"
		online[d.ID] = d.Online
	}
	var out []SSHEndpoint
	for _, s := range all {
		if !strings.HasPrefix(s.Name, sshHostPrefix) {
			continue
		}
		var ep SSHEndpoint
		if json.Unmarshal(s.Value, &ep) != nil || ep.Address == "" || !state[ep.DeviceID] {
			continue
		}
		ep.Online = online[ep.DeviceID]
		out = append(out, ep)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// FindSSHEndpoint picks a machine by (part of) its name, address or id.
func (e *Engine) FindSSHEndpoint(ref string) (SSHEndpoint, error) {
	eps, err := e.SSHEndpoints()
	if err != nil {
		return SSHEndpoint{}, err
	}
	var hits []SSHEndpoint
	for _, ep := range eps {
		switch MachineMatches(ref, ep.Name, ep.DeviceID, ep.Address) {
		case 2:
			return ep, nil
		case 1:
			hits = append(hits, ep)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return SSHEndpoint{}, fmt.Errorf("no machine %q accepts SSH (hearth ssh lists them)", ref)
	}
	var names []string
	for _, h := range hits {
		names = append(names, h.Name)
	}
	return SSHEndpoint{}, fmt.Errorf("%q matches %s; be more specific", ref, strings.Join(names, ", "))
}

// WriteKnownHosts writes the published host keys (address and port)
// into Hearth's known_hosts, so clients verify the machines they reach.
func (e *Engine) WriteKnownHosts(eps []SSHEndpoint) error {
	var b strings.Builder
	for _, ep := range eps {
		if ep.HostKey == "" {
			continue
		}
		host := ep.Address
		if ep.Port != 22 {
			host = fmt.Sprintf("[%s]:%d", ep.Address, ep.Port)
		}
		fmt.Fprintf(&b, "%s %s\n", host, ep.HostKey)
	}
	if err := os.MkdirAll(sshDir(e.Home), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(SSHKnownHosts(e.Home), []byte(b.String()), 0o600)
}

var errNoSSHD = errors.New("no sshd on this machine (install openssh-server)")
