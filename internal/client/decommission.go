package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/mcpcfg"
)

// IsDeviceRemoved reports whether err is the server saying this machine
// was removed from the account (as opposed to an expired login).
func IsDeviceRemoved(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == 410 && he.Body.Error == api.DeviceRemoved
}

// SSH client config block written by `hearth ssh config`.
const (
	SSHConfigBegin = "# >>> hearth ssh (managed; hearth ssh config) >>>"
	SSHConfigEnd   = "# <<< hearth ssh <<<"
)

// decommissionFiles are the files and directories hearth keeps in its home.
// backup/ is left alone: it holds the user's own files hearth replaced.
var decommissionFiles = []string{
	"config.json", "vault.key", "state.json", "server-url", "ca.pem", "net.json",
	"machine.json", "scope.json", "restore-mode.json", "auto-update.json",
	"learn-snooze.json", "env", "agents.local.json", "lib-imported.json",
	"agent-homes.json", "agent-homes", "temp-credentials.json",
	"library", "library-base.bundle", "library-state.json", "library-error.json", "library.lock",
	"render.json", "mcp-state.json", "memory", "shell", "shell-off-here", "shell-state.json",
	"ssh", "site", "site-state.json", "recipe-approved.json", "recipe-done.json",
	"daemon-status.json", "daemon.pid", "daemon.log", "daemon.lock",
}

// Decommission undoes what hearth set up on this machine after it was
// removed from the account: it stops the hearth sshd, takes out the hooks,
// rc blocks, rendered skills/agents and MCP servers hearth wrote, deletes
// temporary credentials, and finally the private data in the hearth home.
// Only things hearth recorded as its own are touched; files the user
// changed since are kept. It returns one line per step.
func Decommission(home string) ([]string, error) {
	var done []string
	var errs []error
	step := func(name string, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			done = append(done, name+": "+err.Error())
			return
		}
		done = append(done, name)
	}
	step("stop hearth sshd and its authorized_keys block", StopSSHD(home))
	_, err := SyncMemoryHooks(home, false)
	step("remove memory hooks", err)
	e := &Engine{Home: home}
	_, err = e.UninstallShell()
	step("remove shell hooks", err)
	_, err = EnsureIdentityRC(home, "")
	step("remove identity rc block", err)
	_, err = RemoveTempCredentials(home, true, nil)
	step("delete temporary credentials", err)
	empty := &mcpcfg.Config{Servers: map[string]mcpcfg.Server{}}
	st := mcpcfg.LoadState(mcpStatePath(home))
	none := func(string) ([]byte, bool, error) { return nil, false, nil }
	_, err = mcpcfg.Render(empty, EnabledMCPTargets(home), none, st, false)
	step("remove MCP servers hearth added", err)
	_, kept, err := library.Unrender(RenderManifest(home))
	step("remove rendered skills and agents", err)
	for _, p := range kept {
		done = append(done, "kept (edited since): "+p)
	}
	step("remove hearth block from ~/.ssh/config", stripSSHConfig())
	if h, err := os.UserHomeDir(); err == nil {
		step("remove hearth ssh keys", os.RemoveAll(filepath.Join(h, ".ssh", "hearth", sha(home)[:12])))
	}
	for _, f := range decommissionFiles {
		if err := os.RemoveAll(filepath.Join(home, f)); err != nil {
			errs = append(errs, err)
		}
	}
	done = append(done, "delete hearth data in "+home)
	_ = os.Remove(home) // only when nothing else is left in it
	return done, errors.Join(errs...)
}

func stripSSHConfig() error {
	h, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(h, ".ssh", "config")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	text := string(b)
	i := strings.Index(text, SSHConfigBegin)
	j := strings.Index(text, SSHConfigEnd)
	if i < 0 || j < i {
		return nil
	}
	text = text[:i] + strings.TrimPrefix(text[j+len(SSHConfigEnd):], "\n")
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), fi.Mode().Perm())
}
