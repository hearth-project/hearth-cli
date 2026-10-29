package client

import (
	"fmt"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/api"
)

// One rule for naming a machine in every command (hearth ssh, machine
// inspect, ...): its full name, its short name (up to the first dot), the
// start of its device id, or a part of its name that only it has.

// MachineMatches reports how ref names a machine: 2 = exactly (name, short
// name, id prefix or address), 1 = part of its name, 0 = not at all.
func MachineMatches(ref, name, id, addr string) int {
	if ref == "" {
		return 0
	}
	switch {
	case ref == name, ref == shortName(name), ref == addr, len(ref) >= 4 && strings.HasPrefix(id, ref):
		return 2
	case strings.Contains(strings.ToLower(name), strings.ToLower(ref)):
		return 1
	}
	return 0
}

// FindDevice picks the device ref names; active devices win over revoked
// ones with the same name.
func FindDevice(ds []api.Device, ref string) (api.Device, error) {
	var exact, part []api.Device
	for _, d := range ds {
		switch MachineMatches(ref, d.Name, d.ID, d.LastIP) {
		case 2:
			exact = append(exact, d)
		case 1:
			part = append(part, d)
		}
	}
	for _, set := range [][]api.Device{exact, part} {
		var active []api.Device
		for _, d := range set {
			if d.Status == "" || d.Status == "active" {
				active = append(active, d)
			}
		}
		switch {
		case len(active) == 1:
			return active[0], nil
		case len(active) > 1:
			var names []string
			for _, d := range active {
				names = append(names, d.Name+" ("+d.ID[:min(8, len(d.ID))]+")")
			}
			return api.Device{}, fmt.Errorf("%q matches %s; use the id", ref, strings.Join(names, ", "))
		case len(set) > 0:
			return set[0], nil // only revoked ones
		}
	}
	return api.Device{}, fmt.Errorf("no machine %q (hearth machine list)", ref)
}
