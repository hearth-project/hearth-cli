package client

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hearth-project/hearth-cli/internal/api"
)

// A name stands for a machine. A machine whose disk was reset (a container
// image restarted) logs in again as a new device with the same host name,
// and a new machine may be given an old one's name. Either continues the
// old, offline device: its name, the conversations it wrote (they count as
// this machine's from then on) and, when its workspace survived, its
// Hearth home there. The old device is logged out and leaves the list.

// ReplaceDevice makes this device continue device id, an offline machine
// of the same account; alias "" keeps this device's own name. Names the
// server knows (given ones, host names) do it through SetDeviceAlias.
func (a *API) ReplaceDevice(id, alias string) error {
	_, err := a.do("POST", "/v1/devices/self/replace", map[string]string{"device": id, "alias": alias}, nil, nil, nil)
	return err
}

// NameOf is the name a machine goes by: the one the user gave it, else the
// one made from its host name (its SSH host name).
func NameOf(d api.Device) string {
	if d.Alias != "" {
		return d.Alias
	}
	f, _ := d.Machine["facts"].(map[string]any)
	return MachineAlias(d.Name, f)
}

// WorkspaceOfDevice is the workspace a machine reported ("" = none).
func WorkspaceOfDevice(d api.Device) string {
	ws, _ := d.Machine["workspace"].(string)
	return ws
}

func activeDevice(d api.Device) bool { return d.Status == "" || d.Status == "active" }

// PreviousSelf finds what this machine was before it was reinstalled: an
// offline device of the account with this device's host name (name), not
// this device (self); the one seen last.
func PreviousSelf(ds []api.Device, self, name string) (api.Device, bool) {
	var best api.Device
	found := false
	for _, d := range ds {
		if d.ID == self || !activeDevice(d) || d.Online || !strings.EqualFold(d.Name, name) {
			continue
		}
		if !found || d.LastSeen > best.LastSeen {
			best, found = d, true
		}
	}
	return best, found
}

// DeviceNamed finds the other machine (not self) that goes by name: the
// name it was given, else its host name or the name made from it.
func DeviceNamed(ds []api.Device, self, name string) (api.Device, bool) {
	for _, d := range ds {
		if d.ID != self && activeDevice(d) && (NameOf(d) == name || d.Alias == "" && d.Name == name) {
			return d, true
		}
	}
	return api.Device{}, false
}

// NameMachine names device id. When an offline machine goes by that name,
// id continues it (returned): the server does that for the names it knows;
// a name only made from a host name is sent as a takeover (this device
// only). An online machine keeps its name (409).
func (e *Engine) NameMachine(id, alias string) (*api.Device, error) {
	var old *api.Device
	if alias != "" {
		if ds, err := e.API.Devices(); err == nil {
			if d, ok := DeviceNamed(ds, id, alias); ok && !d.Online {
				old = &d
			} else if ok {
				// Online under that name (also one only made from its host
				// name, which the server does not compare).
				return nil, &HTTPError{Status: http.StatusConflict, Body: api.Error{Error: "another of your machines has that name"}}
			}
		}
	}
	madeName := old != nil && old.Alias == "" && old.Name != alias
	if madeName && id != e.Cfg.DeviceID {
		old = nil // another machine: the server only compares the names it knows
	}
	if old != nil && madeName {
		if err := e.API.ReplaceDevice(old.ID, alias); err != nil {
			return nil, err
		}
	} else if err := e.API.SetDeviceAlias(id, alias); err != nil {
		return nil, err
	}
	if old != nil {
		_ = e.DeleteSecret(sshHostPrefix + old.ID)
	}
	return old, nil
}

// TakeOver makes this machine continue old (offline): it gets
// old's name, unless both have the same host name and old had none of its
// own (then the generated one is the same), and old's conversations.
// Returns the name it was given ("" = it keeps the generated one).
func (e *Engine) TakeOver(old api.Device) (string, error) {
	if old.ID == e.Cfg.DeviceID {
		return "", fmt.Errorf("%s is this machine", DeviceLabel(old))
	}
	if old.Online {
		return "", fmt.Errorf("%s is online; only an offline machine can be continued by another", DeviceLabel(old))
	}
	alias := old.Alias
	if alias == "" && !strings.EqualFold(old.Name, e.Cfg.DeviceName) {
		alias = NameOf(old)
	}
	if err := e.API.ReplaceDevice(old.ID, alias); err != nil {
		return "", err
	}
	// Its SSH entry goes with it (the list leaves out removed devices anyway).
	_ = e.DeleteSecret(sshHostPrefix + old.ID)
	return alias, nil
}

// MoveHomeOver is MoveHome for a machine that continues device replaced:
// when ws still holds that device's Hearth home (the workspace survived
// the reinstall), the home is kept, with its agent homes, library and
// state, and only this login (config.json, vault.key) and what the home
// lacks move in. Otherwise it is MoveHome. Reports whether a home was
// taken over.
func MoveHomeOver(from, ws, replaced string) (string, bool, error) {
	abs, err := filepath.Abs(ws)
	if err != nil {
		return "", false, err
	}
	target := WorkspaceHome(abs)
	if replaced == "" || filepath.Clean(target) == filepath.Clean(from) || OwnState(target) == "" {
		nh, err := MoveHome(from, ws)
		return nh, false, err
	}
	if cfg, err := LoadConfig(target); err != nil || cfg.DeviceID != replaced {
		nh, err := MoveHome(from, ws) // someone else's home: MoveHome refuses
		return nh, false, err
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return "", false, err
	}
	for _, en := range entries {
		name := en.Name()
		if name == "opt" {
			continue // tools stay where their wrappers point (see MoveHome)
		}
		src, dst := filepath.Join(from, name), filepath.Join(target, name)
		if name == "config.json" || name == "vault.key" {
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return "", false, err
			}
		} else if _, err := os.Lstat(dst); err == nil {
			if err := os.RemoveAll(src); err != nil {
				return "", false, err
			}
			continue // the old home's copy wins
		}
		if err := moveEntry(src, dst); err != nil {
			return "", false, fmt.Errorf("move %s to %s: %w", src, dst, err)
		}
	}
	if rest, _ := os.ReadDir(from); len(rest) == 0 {
		_ = os.Remove(from)
	}
	if _, err := InitWorkspace(abs); err != nil {
		return "", false, err
	}
	rehomeRC(from, target)
	return target, true, nil
}

// moveEntry renames src to dst, or copies and removes it across disks.
func moveEntry(src, dst string) error {
	err := os.Rename(src, dst)
	var le *os.LinkError
	if err == nil || !errors.As(err, &le) || !errors.Is(le.Err, syscall.EXDEV) {
		return err
	}
	if err := copyTreeX(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}
