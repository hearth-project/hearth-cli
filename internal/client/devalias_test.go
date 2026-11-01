package client

import (
	"testing"
)

// A machine the user named is shown as "alias (name)", found by its alias,
// and names are unique among the user's machines.
func TestDeviceAlias(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("gpu-node-7.example"), en.machine("gpu-node-8.example")
	if err := a.API.SetDeviceAlias("self", "h800-07"); err != nil {
		t.Fatal(err)
	}
	if err := b.API.SetDeviceAlias("self", "h800-07"); StatusOf(err) != 409 {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := b.API.SetDeviceAlias("self", "bad name!"); StatusOf(err) != 400 {
		t.Fatalf("invalid name: %v", err)
	}
	if err := b.API.SetDeviceAlias(a.Cfg.DeviceID, "x"); err != nil { // own account: any of its machines
		t.Fatal(err)
	}
	if err := b.API.SetDeviceAlias(a.Cfg.DeviceID, "h800-07"); err != nil {
		t.Fatal(err)
	}
	ds, err := b.API.Devices()
	if err != nil {
		t.Fatal(err)
	}
	d, err := FindDevice(ds, "h800-07")
	if err != nil || d.ID != a.Cfg.DeviceID {
		t.Fatalf("find by alias: %+v %v", d, err)
	}
	if l := DeviceLabel(d); l != "h800-07 (gpu-node-7.example)" {
		t.Fatalf("label %q", l)
	}
	if err := a.API.SetDeviceAlias("self", ""); err != nil {
		t.Fatal(err)
	}
	ds, _ = a.API.Devices()
	d, _ = FindDevice(ds, a.Cfg.DeviceID[:8])
	if DeviceLabel(d) != "gpu-node-7.example" {
		t.Fatalf("cleared: %q", DeviceLabel(d))
	}
}
