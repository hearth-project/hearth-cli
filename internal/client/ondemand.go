package client

import (
	"os"
	"path/filepath"
)

// On-demand machines: a new machine set up with `hearth setup` gets the
// login, its working directory, SSH from the user's devices and background
// upload, nothing else. Credentials (file secrets, recipe files, the
// credential files of skills), conversations, shell lines and the agent
// setup are restored when the user or their agent asks for them. Most work
// on such a machine happens over SSH from another device, and on accounts
// several people share every credential written to disk is readable by
// all of them. Machines set up before keep restoring as they did (no
// marker); `hearth setup --full` restores everything as before.

type restoreMode struct {
	Mode string `json:"mode"` // "on-demand"
}

func restoreModePath(home string) string { return filepath.Join(home, "restore-mode.json") }

// OnDemand reports whether this machine restores credentials and agent
// state only when asked.
func OnDemand(home string) bool {
	var m restoreMode
	return loadJSON(restoreModePath(home), &m) == nil && m.Mode == "on-demand"
}

// SetOnDemand records the restore mode of this machine (not synced).
func SetOnDemand(home string, on bool) error {
	if !on {
		err := os.Remove(restoreModePath(home))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	b, err := jsonIndent(restoreMode{Mode: "on-demand"})
	if err != nil {
		return err
	}
	return writeFileAtomic(restoreModePath(home), b, 0o600)
}

// agentCLI names the command each agent is run with.
var agentCLI = map[string]string{"codex": "codex", "claude": "claude", "comate": "comate"}

// AgentInstalledHere reports whether an agent's CLI is on this machine.
func AgentInstalledHere(agent string) bool {
	cli, ok := agentCLI[agent]
	if !ok {
		return false
	}
	_, err := LookTool(cli)
	return err == nil
}

// InstalledAgents keeps the agents of list whose CLI is on this machine.
func InstalledAgents(list []string) []string {
	out := []string{}
	for _, a := range list {
		if AgentInstalledHere(a) {
			out = append(out, a)
		}
	}
	return out
}

// AdoptInstalledAgents: on an on-demand machine, an agent the user works
// with that got installed since (the user restored its environment) joins
// this machine's agents, so the library is rendered for it and its
// hooks and skill search are registered. Returns the agents added.
func AdoptInstalledAgents(home string, used []string) ([]string, error) {
	if !OnDemand(home) {
		return nil, nil
	}
	have := machineSet(home)
	on := enabledSet(home)
	var added []string
	for _, a := range used {
		if on[a] && !have[a] && AgentInstalledHere(a) {
			if err := AddMachineAgent(home, a); err != nil {
				return added, err
			}
			have[a] = true
			added = append(added, a)
		}
	}
	return added, nil
}
