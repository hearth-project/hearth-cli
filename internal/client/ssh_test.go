package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hearth-project/hearth-cli/internal/api"
)

func TestManagedKeysBlock(t *testing.T) {
	own := "ssh-ed25519 AAAAown laptop\n"
	k1, k2 := testKeyLine(t, "a"), testKeyLine(t, "b")
	const me = "me@example.org"
	got := setManagedKeys(own, me, []string{k1, k2}, nil)
	if !strings.HasPrefix(got, own) || len(managedKeys(got, me)) != 2 {
		t.Fatalf("block not added:\n%s", got)
	}
	if again := setManagedKeys(got, me, []string{k1, k2}, nil); again != got {
		t.Fatalf("not idempotent:\n%s", again)
	}
	one := setManagedKeys(got, me, []string{k1}, nil)
	if k := managedKeys(one, me); len(k) != 1 || !strings.HasPrefix(one, own) {
		t.Fatalf("replace: %q\n%s", k, one)
	}
	if none := setManagedKeys(one, me, nil, nil); none != own {
		t.Fatalf("remove left %q", none)
	}
}

// Two Hearth accounts on one Unix account (a shared root in a GPU
// container) keep a block each; neither daemon takes out the other's keys.
// The old block without an owner is converted by the person whose keys it
// holds and left alone by everyone else.
func TestManagedKeysBlockPerAccount(t *testing.T) {
	a1, a2, b1 := testKeyLine(t, "a1"), testKeyLine(t, "a2"), testKeyLine(t, "b1")
	const a, b = "a@example.org", "b@example.org"
	text := setManagedKeys("", a, []string{a1}, nil)
	text = setManagedKeys(text, b, []string{b1}, nil)
	text = setManagedKeys(text, a, []string{a1, a2}, nil)
	if len(managedKeys(text, a)) != 2 || len(managedKeys(text, b)) != 1 {
		t.Fatalf("one account replaced the other's block:\n%s", text)
	}
	if out := setManagedKeys(text, a, nil, nil); len(managedKeys(out, b)) != 1 || len(managedKeys(out, a)) != 0 {
		t.Fatalf("removing a's block touched b's:\n%s", out)
	}
	// An older client of b wrote the old block; a's daemon leaves it alone.
	legacy := akBegin + "\n" + b1 + "\n" + akEnd + "\n"
	text = setManagedKeys(legacy, a, []string{a1}, nil)
	if !strings.Contains(text, akBegin) || len(managedKeys(text, a)) != 1 {
		t.Fatalf("another person's old block removed:\n%s", text)
	}
	// b's new client converts its own old block, with an option prefix.
	text = setManagedKeys(text, b, []string{`expiry-time="20990101" ` + b1}, nil)
	if strings.Contains(text, akBegin) || len(managedKeys(text, b)) != 1 || len(managedKeys(text, a)) != 1 {
		t.Fatalf("own old block not converted:\n%s", text)
	}
	// Stopping identifies the old block by this device's key alone.
	if out := setManagedKeys(legacy, b, nil, []string{b1}); strings.Contains(out, akBegin) {
		t.Fatalf("own old block kept on stop:\n%s", out)
	}
}

func testKeyLine(t *testing.T, comment string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk))) + " " + comment
}

func TestStrictOK(t *testing.T) {
	d := t.TempDir()
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if strictOK(filepath.Join(d, "x", "authorized_keys")) {
		t.Fatal("a missing directory passed")
	}
	if strictOK("/tmp/authorized_keys") {
		t.Fatal("/tmp is writable by others")
	}
}

// Every device publishes its Hearth key and its usual ~/.ssh keys; a
// revoked device's keys are no longer accepted anywhere.
func TestSSHKeysFollowDevices(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	h, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Join(h, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	usual := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHvrzHkHVdSCQwVVpOvM60RZEbnxFIQ4kq0RJozxYOLb me\n"
	if err := os.WriteFile(filepath.Join(h, ".ssh", "id_ed25519.pub"), []byte(usual), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, m := range []*machine{a, b} {
		if _, err := m.PublishDeviceKey(); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := a.SSHKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 4 { // two Hearth keys, the usual key of each
		t.Fatalf("keys = %d, want 4: %+v", len(keys), keys)
	}
	if err := a.API.RevokeDevice(b.Cfg.DeviceID); err != nil {
		t.Fatal(err)
	}
	keys, _ = a.SSHKeys()
	for _, k := range keys {
		if k.owner() == b.Cfg.DeviceID {
			t.Fatalf("revoked device key still accepted: %+v", k)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("keys after revoke = %d, want 2", len(keys))
	}
	// The revoked device's keys and endpoint are deleted at the next
	// publish, so they stop counting toward the secret limit; a key added
	// by hand stays.
	if err := b.SetSecret(Secret{Name: sshHostPrefix + b.Cfg.DeviceID, Value: []byte(`{"device_id":"` + b.Cfg.DeviceID + `"}`)}); err == nil {
		t.Fatal("a revoked device still writes secrets")
	}
	gone := `{"device_id":"0000000000000000","address":"10.0.0.9","port":8022}`
	if err := a.SetSecret(Secret{Name: sshHostPrefix + "0000000000000000", Value: []byte(gone)}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AuthorizeKey(usual, "laptop"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PublishDeviceKey(); err != nil {
		t.Fatal(err)
	}
	all, err := a.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	manual := 0
	for _, s := range all {
		if strings.Contains(s.Name, b.Cfg.DeviceID) || s.Name == sshHostPrefix+"0000000000000000" {
			t.Fatalf("dead device secret kept: %s", s.Name)
		}
		if strings.HasPrefix(s.Name, sshKeyPrefix+"x-") {
			manual++
		}
	}
	if manual != 1 {
		t.Fatalf("key added by hand pruned: %d", manual)
	}
	if err := a.UnauthorizeKey("laptop"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h, ".ssh", "id_ed25519.pub")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PublishDeviceKey(); err != nil {
		t.Fatal(err)
	}
	if keys, _ = a.SSHKeys(); len(keys) != 1 {
		t.Fatalf("removed usual key still published: %+v", keys)
	}
}

func TestFindDevice(t *testing.T) {
	ds := []api.Device{
		{ID: "f5da6288e7b09482", Name: "10.0.0.1", Status: "revoked"},
		{ID: "e34b73cb58dc6c8e", Name: "10.0.0.1", Status: "active"},
		{ID: "3a7c31806665c8c2", Name: "gpu-f04-14.example-host", Status: "active"},
		{ID: "205cd684faf655cf", Name: "gpu-f04-15.example-host", Status: "active"},
	}
	for ref, want := range map[string]string{"gpu-f04-14": "3a7c31806665c8c2", "gpu-f04-14.example-host": "3a7c31806665c8c2",
		"3a7c": "3a7c31806665c8c2", "10.0.0.1": "e34b73cb58dc6c8e", "f04-15": "205cd684faf655cf"} {
		d, err := FindDevice(ds, ref)
		if err != nil || d.ID != want {
			t.Errorf("%s: %s %v", ref, d.ID, err)
		}
	}
	if _, err := FindDevice(ds, "gpu-f04"); err == nil {
		t.Error("ambiguous name accepted")
	}
}

// Accepted device keys carry an expiry the daemon renews ([ssh] key_days).
func TestKeyLinesExpiry(t *testing.T) {
	keys := []SSHKey{{Key: "ssh-ed25519 AAAA1 a"}}
	if got := keyLines(keys, "20261108"); len(got) != 1 || got[0] != `expiry-time="20261108" ssh-ed25519 AAAA1 a` {
		t.Fatalf("%v", got)
	}
	if got := keyLines(keys, ""); got[0] != "ssh-ed25519 AAAA1 a" {
		t.Fatalf("%v", got)
	}
	home := t.TempDir()
	now := time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)
	if sshdSupportsExpiry() {
		if e := keyExpiry(home, now); e != "20261108" {
			t.Fatalf("default 7 days: %q", e)
		}
	}
	os.MkdirAll(LibraryDir(home), 0o755)
	os.WriteFile(filepath.Join(LibraryDir(home), "config.toml"), []byte("[ssh]\nkey_days = 0\nforward_agent = false\n"), 0o644)
	if e := keyExpiry(home, now); e != "" {
		t.Fatalf("key_days = 0: %q", e)
	}
	if SSHForwardAgent(home) {
		t.Fatal("forward_agent = false ignored")
	}
}

// sshd listens on every address of the machine the candidates name, in
// their order, each once; addresses of other machines are dropped.
func TestSSHCandidates(t *testing.T) {
	local := map[string]bool{"10.0.0.5": true, "33.1.2.3": true, "10.79.1.1": true}
	got := sshCandidates(local, nil, []string{"10.79.1.1", "8.8.8.8"}, []string{"33.1.2.3"}, []string{"33.1.2.3", "10.79.1.1"})
	if strings.Join(got, ",") != "10.79.1.1,33.1.2.3" {
		t.Fatalf("got %v", got)
	}
	if got := sshCandidates(local, []string{"10.0.0.5"}, []string{"10.79.1.1"}); got[0] != "10.0.0.5" {
		t.Fatalf("bond0 first: %v", got)
	}
}

// A client connects to the first address that answers; with none it
// falls back to the main one.
func TestReachableAddress(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	ep := SSHEndpoint{Address: "192.0.2.1", Addresses: []string{"192.0.2.1", "127.0.0.1"}, Port: port}
	if a, ok := ep.ReachableAddress(300 * time.Millisecond); !ok || a != "127.0.0.1" {
		t.Fatalf("got %s %v", a, ok)
	}
	l.Close()
	if a, ok := ep.ReachableAddress(300 * time.Millisecond); ok || a != "192.0.2.1" {
		t.Fatalf("none answers: got %s %v", a, ok)
	}
	if got := (SSHEndpoint{Address: "10.0.0.1"}).Addrs(); len(got) != 1 || got[0] != "10.0.0.1" {
		t.Fatalf("old endpoint: %v", got)
	}
}
