package client

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	mach "github.com/hearth-project/hearth-cli/internal/machine"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// Machine profiles (library/machines.toml) are synced; the profiles chosen
// by hand for this machine and the detected facts are local.

// MachinesPath is the synced profiles file.
func MachinesPath(home string) string { return filepath.Join(LibraryDir(home), "machines.toml") }

func machineLocalPath(home string) string { return filepath.Join(home, "machine.json") }

type machineLocal struct {
	Use []string `json:"use,omitempty"` // profiles chosen with `hearth machine use`
}

// ChosenProfiles returns the profiles chosen by hand on this machine.
func ChosenProfiles(home string) []string {
	var m machineLocal
	loadJSON(machineLocalPath(home), &m)
	return m.Use
}

// SetChosenProfiles records the profiles chosen by hand.
func SetChosenProfiles(home string, names []string) error {
	b, err := jsonIndent(machineLocal{Use: names})
	if err != nil {
		return err
	}
	return writeFileAtomic(machineLocalPath(home), b, 0o600)
}

var factsCache struct {
	sync.Mutex
	f   *mach.Facts
	at  time.Time
	env string
}

// Facts detects this machine once per process (nvidia-smi is not free).
func Facts() mach.Facts {
	factsCache.Lock()
	defer factsCache.Unlock()
	if factsCache.f == nil || time.Since(factsCache.at) > 10*time.Minute || factsCache.env != os.Getenv("HEARTH_MACHINE") {
		factsCache.env = os.Getenv("HEARTH_MACHINE")
		f := mach.Detect()
		factsCache.f, factsCache.at = &f, time.Now()
	}
	return *factsCache.f
}

// MachineState is this machine as recipes see it.
type MachineState struct {
	Facts    mach.Facts
	Profiles []mach.Profile // active, chosen first
	File     *mach.File
}

// Names of the active profiles.
func (m MachineState) Names() []string { return mach.Names(m.Profiles) }

// Workspace is the first active profile's working directory ("" = none).
func (m MachineState) Workspace() string {
	for _, p := range m.Profiles {
		if p.Workspace != "" {
			return p.Workspace
		}
	}
	return ""
}

// Machine loads the profiles and matches them against this machine.
func Machine(home string) (MachineState, error) {
	f, err := mach.Load(MachinesPath(home))
	if err != nil {
		return MachineState{Facts: Facts(), File: &mach.File{}}, err
	}
	facts := Facts()
	return MachineState{Facts: facts, Profiles: f.Active(facts, ChosenProfiles(home)), File: f}, nil
}

// profileWorkspace returns the active profile's workspace, detecting the
// machine only when some profile defines one.
func profileWorkspace(home string) string {
	f, err := mach.Load(MachinesPath(home))
	if err != nil {
		return ""
	}
	has := false
	for _, p := range f.Profiles {
		has = has || p.Workspace != ""
	}
	if !has {
		return ""
	}
	for _, p := range f.Active(Facts(), ChosenProfiles(home)) {
		if p.Workspace != "" {
			return expandWorkspace(p.Workspace)
		}
	}
	return ""
}

// expandWorkspace resolves ~ and ${HOME} ($USER too, for shared disks).
func expandWorkspace(p string) string {
	p = os.Expand(p, func(k string) string {
		if k == "HOME" {
			h, _ := os.UserHomeDir()
			return h
		}
		return os.Getenv(k)
	})
	return paths.Expand(p)
}

// EnsureProfileWorkspace activates and creates the profile workspace (after
// the library was pulled on a new machine). Returns it, or "".
func EnsureProfileWorkspace(home string) (string, error) {
	if os.Getenv("HEARTH_WORKSPACE") != "" && os.Getenv("HEARTH_WORKSPACE_FROM_PROFILE") == "" {
		return "", nil // an explicit workspace (init / --workspace / env) wins
	}
	ws := profileWorkspace(home)
	if ws == "" {
		return "", nil
	}
	os.Setenv("HEARTH_WORKSPACE", ws)
	os.Setenv("HEARTH_WORKSPACE_FROM_PROFILE", "1")
	return ws, os.MkdirAll(ws, 0o755)
}
