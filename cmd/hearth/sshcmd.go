package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

func sshCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "ssh [machine] [-- command ...]",
		Short: "SSH to your machines with Hearth managing the keys (no machine: list them)",
		Long: "Every Linux machine of your account accepts SSH from your own devices: Hearth gives each device a key,\n" +
			"keeps the public keys (plus each device's ~/.ssh/id_*.pub) end-to-end encrypted, writes the keys of your\n" +
			"active devices into a marked block of ~/.ssh/authorized_keys, and runs the system sshd the way it is set\n" +
			"up by hand on GPU containers (public keys only, main interface only, port 8022; an sshd already listening\n" +
			"there is used as it is). Revoking a device removes its keys everywhere within a minute.\n\n" +
			"  hearth ssh                 machines you can reach\n" +
			"  hearth ssh <machine>       log in (part of the name is enough)\n" +
			"  hearth ssh config          Host entries in ~/.ssh/config (VS Code / Cursor Remote-SSH, scp, rsync)",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if _, err := e.PublishDeviceKey(); err != nil {
				return err
			}
			eps, err := e.SSHEndpoints()
			if err != nil {
				return err
			}
			if err := e.WriteKnownHosts(eps); err != nil {
				return err
			}
			if len(args) == 0 {
				if len(eps) == 0 {
					fmt.Println("no machine accepts SSH yet: machines turn it on when their hearth daemon runs (Linux; hearth ssh status there)")
					return nil
				}
				t := &table{}
				t.add("MACHINE", "STATE", "CONNECT", "PLAIN SSH (your usual key)")
				names := sshNames(eps)
				for i, ep := range eps {
					st := "offline"
					if ep.Online {
						st = "online"
					}
					t.add(ep.Name, st, "hearth ssh "+names[i], fmt.Sprintf("ssh -p %d %s@%s", ep.Port, ep.User, ep.Address))
				}
				t.fit()
				t.print()
				fmt.Println("VS Code / Cursor Remote-SSH, scp, rsync: hearth ssh config adds them to ~/.ssh/config as hearth-<machine>")
				return nil
			}
			ep, err := e.FindSSHEndpoint(args[0])
			if err != nil {
				return err
			}
			bin, err := exec.LookPath("ssh")
			if err != nil {
				return fmt.Errorf("no ssh client on this machine")
			}
			argv := append([]string{"ssh"}, sshArgs(e.Home, ep)...)
			argv = append(argv, ep.User+"@"+ep.Address)
			argv = append(argv, args[1:]...)
			return syscall.Exec(bin, argv, os.Environ())
		},
	}
	var all bool
	setOn := func(on bool) func(*cobra.Command, []string) error {
		return func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if all {
				if err := client.SetSSHConfig(e.Home, on); err != nil {
					return err
				}
				if _, err := e.LibPush(false); err != nil {
					fmt.Printf("library not pushed: %v\n", err)
				}
			}
			if err := client.SetSSHHere(e.Home, on); err != nil {
				return err
			}
			st, err := e.EnsureSSHD()
			printSSHStatus(st)
			return err
		}
	}
	enable := &cobra.Command{Use: "enable", Short: "Accept SSH on this machine (default on Linux)", RunE: setOn(true)}
	disable := &cobra.Command{Use: "disable", Short: "Stop accepting SSH on this machine (--all: on every machine of the account)", RunE: setOn(false)}
	disable.Flags().BoolVar(&all, "all", false, "every machine of the account (synced setting)")
	enable.Flags().BoolVar(&all, "all", false, "every machine of the account (synced setting)")
	var keyName string
	authorize := &cobra.Command{Use: "authorize <public key | file>", Short: "Also accept a key that is not a Hearth device (e.g. an existing laptop key)", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			line := a[0]
			if b, err := os.ReadFile(recipe.ExpandHome(a[0])); err == nil {
				line = string(b)
			}
			k, err := e.AuthorizeKey(line, keyName)
			if err != nil {
				return err
			}
			fmt.Printf("authorized %s (%s) on all your machines within a minute\n", k.Name, k.ID)
			return nil
		}}
	authorize.Flags().StringVar(&keyName, "name", "", "a name for the key (default: its comment)")
	c.AddCommand(enable, disable, authorize,
		&cobra.Command{Use: "status", Short: "This machine's sshd: running, address, accepted keys", RunE: func(*cobra.Command, []string) error {
			printSSHStatus(client.SSHServerStatus(client.Home()))
			return nil
		}},
		&cobra.Command{Use: "keys", Short: "Keys your machines accept", RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if _, err := e.PublishDeviceKey(); err != nil {
				return err
			}
			keys, err := e.SSHKeys()
			if err != nil {
				return err
			}
			t := &table{}
			t.add("NAME", "KIND", "ID", "KEY")
			for _, k := range keys {
				kind := "device"
				if !k.Device {
					kind = "added (hearth ssh unauthorize)"
				}
				f := strings.Fields(k.Key)
				short := f[0]
				if len(f) > 1 && len(f[1]) > 16 {
					short += " …" + f[1][len(f[1])-12:]
				}
				t.add(k.Name, kind, k.ID, short)
			}
			t.fit()
			t.print()
			return nil
		}},
		&cobra.Command{Use: "unauthorize <id|name>", Short: "Remove a key added with authorize", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if err := e.UnauthorizeKey(a[0]); err != nil {
				return err
			}
			fmt.Println("removed; your machines stop accepting it within a minute")
			return nil
		}},
		&cobra.Command{Use: "config", Short: "Write Host entries for your machines into ~/.ssh/config (kept up to date by the daemon)", RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if _, err := e.PublishDeviceKey(); err != nil {
				return err
			}
			n, path, err := writeSSHConfig(e, true)
			if err != nil {
				return err
			}
			fmt.Printf("%d machine(s) in %s: ssh hearth-<machine>, or pick them in VS Code / Cursor Remote-SSH\n", n, path)
			return nil
		}},
	)
	return c
}

// sshNames gives each machine a short name to connect with: its host
// name, or the start of its device id when two machines share a name.
func sshNames(eps []client.SSHEndpoint) []string {
	n := map[string]int{}
	for _, ep := range eps {
		n[shortHost(ep.Name)]++
	}
	out := make([]string, len(eps))
	for i, ep := range eps {
		out[i] = shortHost(ep.Name)
		if n[out[i]] > 1 {
			out[i] = ep.DeviceID[:min(8, len(ep.DeviceID))]
		}
	}
	return out
}

func sshArgs(home string, ep client.SSHEndpoint) []string {
	return []string{"-i", client.SSHKeyPath(home), "-o", "IdentitiesOnly=yes", "-o", "UserKnownHostsFile=" + client.SSHKnownHosts(home),
		"-o", "StrictHostKeyChecking=yes", "-p", strconv.Itoa(ep.Port)}
}

func printSSHStatus(st client.SSHStatus) {
	switch {
	case !st.On:
		fmt.Println("ssh: off on this machine (hearth ssh enable)")
	case st.Running && st.External:
		fmt.Printf("ssh: accepting %d key(s) of your devices at %s:%d (an sshd started outside Hearth; keys in ~/.ssh/authorized_keys)\n", st.Keys, st.Address, st.Port)
	case st.Running:
		fmt.Printf("ssh: accepting %d key(s) of your devices at %s:%d (sshd pid %d)\n", st.Keys, st.Address, st.Port, st.PID)
	default:
		msg := "not running yet (the daemon starts it within a minute)"
		if st.Err != "" {
			msg = st.Err
		}
		fmt.Println("ssh: " + msg)
	}
}

const sshBlockBegin = "# >>> hearth ssh (managed; hearth ssh config) >>>"
const sshBlockEnd = "# <<< hearth ssh <<<"

// writeSSHConfig puts one Host entry per machine into ~/.ssh/config, inside
// a managed block. Without create it only refreshes an existing block.
func writeSSHConfig(e *client.Engine, create bool) (int, string, error) {
	h, _ := os.UserHomeDir()
	path := filepath.Join(h, ".ssh", "config")
	cur, _ := os.ReadFile(path)
	text := string(cur)
	i, j := strings.Index(text, sshBlockBegin), strings.Index(text, sshBlockEnd)
	if !create && (i < 0 || j < i) {
		return 0, path, nil
	}
	eps, err := e.SSHEndpoints()
	if err != nil {
		return 0, path, err
	}
	if err := e.WriteKnownHosts(eps); err != nil {
		return 0, path, err
	}
	var b strings.Builder
	b.WriteString(sshBlockBegin + "\n")
	names := sshNames(eps)
	for i, ep := range eps {
		fmt.Fprintf(&b, "Host hearth-%s\n  HostName %s\n  Port %d\n  User %s\n  IdentityFile %s\n  IdentitiesOnly yes\n  UserKnownHostsFile %s\n  StrictHostKeyChecking yes\n",
			names[i], ep.Address, ep.Port, ep.User, client.SSHKeyPath(e.Home), client.SSHKnownHosts(e.Home))
	}
	b.WriteString(sshBlockEnd + "\n")
	if i >= 0 && j > i {
		text = text[:i] + b.String() + strings.TrimPrefix(text[j+len(sshBlockEnd):], "\n")
	} else {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += b.String()
	}
	if text == string(cur) {
		return len(eps), path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, path, err
	}
	return len(eps), path, os.WriteFile(path, []byte(text), 0o600)
}
