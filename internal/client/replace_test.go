package client

import (
	"os"
	"path/filepath"
	"testing"
)

// A reinstalled machine (same host name, new device) takes the place of its
// old, offline entry: the old one's name and conversations, the old one
// logged out and out of the list. An online machine cannot be replaced.
func TestReplaceDevice(t *testing.T) {
	en := newEnv(t)
	old := en.machine("gpu-node-7.example")
	if err := old.API.SetDeviceAlias("self", "h800-07"); err != nil {
		t.Fatal(err)
	}
	old.appendLines(t, 5, "old")
	if _, err := old.PushOnce(); err != nil {
		t.Fatal(err)
	}
	other := en.machine("gpu-node-8.example")
	again := en.machine("gpu-node-7.example")

	ds, err := again.API.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := PreviousSelf(ds, again.Cfg.DeviceID, again.Cfg.DeviceName); ok {
		t.Fatal("the old entry is online: not a previous self")
	}
	d, _ := FindDevice(ds, old.Cfg.DeviceID)
	if _, err := again.TakeOver(d); err == nil {
		t.Fatal("replaced an online machine")
	}
	if err := again.API.ReplaceDevice(old.Cfg.DeviceID, ""); StatusOf(err) != 409 {
		t.Fatalf("server let an online machine be replaced: %v", err)
	}
	if err := old.API.SignOff(); err != nil {
		t.Fatal(err)
	}
	ds, _ = again.API.Devices()
	prev, ok := PreviousSelf(ds, again.Cfg.DeviceID, again.Cfg.DeviceName)
	if !ok || prev.ID != old.Cfg.DeviceID {
		t.Fatalf("previous self: %+v %v", prev, ok)
	}
	if _, ok := PreviousSelf(ds, other.Cfg.DeviceID, other.Cfg.DeviceName); ok {
		t.Fatal("another host name is not a previous self")
	}
	alias, err := again.TakeOver(prev)
	if err != nil || alias != "h800-07" {
		t.Fatalf("take over: %q %v", alias, err)
	}
	ds, _ = other.API.Devices()
	d, err = FindDevice(ds, "h800-07")
	if err != nil || d.ID != again.Cfg.DeviceID {
		t.Fatalf("the name goes to the new machine: %+v %v", d, err)
	}
	for _, x := range ds {
		if x.ID == old.Cfg.DeviceID && activeDevice(x) {
			t.Fatal("old entry still listed as active")
		}
	}
	ss, err := again.API.Sessions()
	if err != nil || len(ss) == 0 {
		t.Fatalf("sessions: %v %v", ss, err)
	}
	for _, s := range ss {
		if s.UpdatedBy != again.Cfg.DeviceID {
			t.Fatalf("session %s still the old machine's (%s)", s.ID, s.UpdatedBy)
		}
	}
	if _, err := old.API.Devices(); StatusOf(err) != 401 {
		t.Fatalf("old login still works: %v", err)
	}
	if err := again.API.ReplaceDevice(again.Cfg.DeviceID, ""); StatusOf(err) != 400 {
		t.Fatalf("replace itself: %v", err)
	}
}

// A new machine given an offline machine's name continues it: a given
// name (the server takes it over), and the name made from a host name
// (sent as a takeover). An online machine keeps its name.
func TestNameContinuesOfflineMachine(t *testing.T) {
	en := newEnv(t)
	named := en.machine("gpu-node-7.example")
	if err := named.API.SetDeviceAlias("self", "h800-07"); err != nil {
		t.Fatal(err)
	}
	plain := en.machine("gpu-node-8.example")
	nu := en.machine("gpu-node-9.example")
	if _, err := nu.NameMachine(nu.Cfg.DeviceID, "h800-07"); StatusOf(err) != 409 {
		t.Fatalf("online machine's name: %v", err)
	}
	named.API.SignOff()
	plain.API.SignOff()
	old, err := nu.NameMachine(nu.Cfg.DeviceID, "h800-07")
	if err != nil || old == nil || old.ID != named.Cfg.DeviceID {
		t.Fatalf("given name: %+v %v", old, err)
	}
	if _, err := named.API.Devices(); StatusOf(err) != 401 {
		t.Fatalf("old login still works: %v", err)
	}
	other := en.machine("gpu-node-10.example")
	ds, _ := other.API.Devices()
	d, _ := FindDevice(ds, plain.Cfg.DeviceID)
	made := NameOf(d)
	if made == "" || made == d.Name {
		t.Fatalf("made name %q", made)
	}
	old, err = other.NameMachine(other.Cfg.DeviceID, made)
	if err != nil || old == nil || old.ID != plain.Cfg.DeviceID {
		t.Fatalf("made name: %+v %v", old, err)
	}
	ds, _ = other.API.Devices()
	if d, err := FindDevice(ds, made); err != nil || d.ID != other.Cfg.DeviceID {
		t.Fatalf("name goes to the new machine: %+v %v", d, err)
	}
}

// A workspace that survived the reinstall keeps the old machine's Hearth
// home; only the new login moves in.
func TestMoveHomeOver(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "work")
	target := filepath.Join(ws, ".hearth")
	os.MkdirAll(filepath.Join(target, "agent-homes", "codex"), 0o700)
	os.WriteFile(filepath.Join(target, "agent-homes", "codex", "history.jsonl"), []byte("old\n"), 0o600)
	if err := SaveConfig(target, &Config{Server: "https://s", Email: "a@b", DeviceID: "old1", DeviceName: hostKey()}); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(root, "home", ".hearth")
	os.MkdirAll(from, 0o700)
	if err := SaveConfig(from, &Config{Server: "https://s", Email: "a@b", DeviceID: "new1", DeviceName: hostKey()}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(from, "vault.key"), []byte("k"), 0o600)
	os.WriteFile(filepath.Join(from, "site.json"), []byte("{}"), 0o600)

	if _, _, err := MoveHomeOver(from, ws, "someone-else"); err == nil {
		t.Fatal("moved over a home of another device")
	}
	nh, kept, err := MoveHomeOver(from, ws, "old1")
	if err != nil || !kept || nh != target {
		t.Fatalf("move: %s %v %v", nh, kept, err)
	}
	cfg, err := LoadConfig(target)
	if err != nil || cfg.DeviceID != "new1" {
		t.Fatalf("login: %+v %v", cfg, err)
	}
	for _, f := range []string{"vault.key", "site.json", "agent-homes/codex/history.jsonl"} {
		if _, err := os.Stat(filepath.Join(target, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatalf("old home left behind: %v", err)
	}
}
