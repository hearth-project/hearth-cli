package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/api"
)

func TestManagedKeysBlock(t *testing.T) {
	own := "ssh-ed25519 AAAAown laptop\n"
	got := setManagedKeys(own, []string{"ssh-ed25519 AAAA1 a", "ssh-ed25519 AAAA2 b"})
	if !strings.HasPrefix(got, own) || len(managedKeys(got)) != 2 {
		t.Fatalf("block not added:\n%s", got)
	}
	if again := setManagedKeys(got, []string{"ssh-ed25519 AAAA1 a", "ssh-ed25519 AAAA2 b"}); again != got {
		t.Fatalf("not idempotent:\n%s", again)
	}
	one := setManagedKeys(got, []string{"ssh-ed25519 AAAA1 a"})
	if k := managedKeys(one); len(k) != 1 || !strings.HasPrefix(one, own) {
		t.Fatalf("replace: %q\n%s", k, one)
	}
	if none := setManagedKeys(one, nil); none != own {
		t.Fatalf("remove left %q", none)
	}
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
