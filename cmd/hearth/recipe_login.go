package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/proxyenv"
)

// Interactive restore steps (a login that shows a QR code) run in the
// user's own terminal: `hearth recipe login <key>` runs the step's command
// in the foreground, so the code is drawn by the program itself, and when
// it succeeds runs the step's check and marks the step done. For another
// machine the agent hands the user the command `hearth ssh -t <machine> --
// ...` (hearth recipe login <key> --on <machine> prints it). Only plan
// steps that are manual, tty = true and name their command (run) can run.

// secretRefRE: a Hearth secret in a step's environment, ${secret:NAME}.
var secretRefRE = regexp.MustCompile(`\$\{secret:([A-Za-z0-9][A-Za-z0-9._-]{0,127})\}`)

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// stepKeyRE: a plan step's key (recipe/hash); safe to put in a command line.
var stepKeyRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// shellWord quotes s for a shell only when it needs it.
func shellWord(s string) string {
	if regexp.MustCompile(`^[A-Za-z0-9._@:/=,+-]+$`).MatchString(s) {
		return s
	}
	return shellQuote(s)
}

// isThisMachine: --on names the machine this command runs on.
func isThisMachine(e *client.Engine, on string) bool {
	if on == "" {
		return true
	}
	ds, err := e.API.Devices()
	if err != nil {
		return false
	}
	d, err := client.FindDevice(ds, on)
	return err == nil && d.ID == e.Cfg.DeviceID
}

// loginScript is what runs on the machine that is logged in: hearth may
// sit in ~/.local/bin, which a non-login shell does not have on PATH.
func loginScript(key string, extra ...string) string {
	bin := "hearth"
	if b := os.Getenv("HEARTH_LOGIN_REMOTE_BIN"); b != "" {
		bin = shellQuote(b) // a test build on the target
	}
	return `PATH="$HOME/.local/bin:$PATH"; exec ` + bin + ` recipe login ` + key + strings.Join(append([]string{""}, extra...), " ")
}

// loginSSHArgs are the arguments of hearth ssh that run script on machine (one
// argument: ssh joins them and the machine's shell runs the result).
func loginSSHArgs(machine string, tty bool, script string) []string {
	a := []string{"ssh"}
	if tty {
		a = append(a, "-t")
	}
	return append(a, machine, "--", script)
}

// sshLine is the same as a command line for the user's shell.
func sshLine(machine string, script string) string {
	return "hearth ssh -t " + shellWord(machine) + " -- " + shellQuote(script)
}

func recipeLoginCmd() *cobra.Command {
	var on string
	var print, check bool
	c := &cobra.Command{Use: "login <step-key>", Short: "Run a QR-code login step in this terminal (--on <machine>: over hearth ssh); without a terminal, print the command for the user",
		Long: "Runs an interactive plan step (manual, tty = true, with run) in the foreground of this terminal, so the\n" +
			"program draws its QR code itself; when the command succeeds the step's check runs and the step is marked\n" +
			"done. With --on <machine> it goes over hearth ssh -t. Without a terminal (an agent runs it), or with\n" +
			"--print, it prints the command the user runs in their own terminal instead:\n\n" +
			"  hearth ssh -t <machine> -- 'PATH=\"$HOME/.local/bin:$PATH\"; exec hearth recipe login <key>'",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			key := a[0]
			if !stepKeyRE.MatchString(key) {
				return fmt.Errorf("%q is not a step key (hearth recipe plan)", key)
			}
			e, err := engine()
			if err != nil {
				return err
			}
			local := isThisMachine(e, on)
			if check { // can this machine run it? (the agent asks before it hands out a command)
				_, _, err := e.InteractiveStep(key)
				return err
			}
			here := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
			if !local && (print || !here) {
				if err := verifyRemoteLogin(on, key); err != nil {
					return err
				}
			}
			switch {
			case local && (print || !here):
				if _, _, err := e.InteractiveStep(key); err != nil {
					return err
				}
				printLoginCommand("hearth recipe login "+key, "")
				return nil
			case local:
				return runLoginHere(e, key)
			case print || !here:
				printLoginCommand(sshLine(on, loginScript(key)), on)
				return nil
			}
			self, err := os.Executable()
			if err != nil {
				return err
			}
			cmd := exec.Command(self, loginSSHArgs(on, true, loginScript(key))...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					os.Exit(ee.ExitCode()) // the machine said why already
				}
				return err
			}
			return nil
		}}
	c.Flags().StringVar(&on, "on", "", "the machine (default: this one)")
	c.Flags().BoolVar(&print, "print", false, "only print the command for the user's terminal")
	c.Flags().BoolVar(&check, "check", false, "only check that this machine can run the step (exit status)")
	_ = c.Flags().MarkHidden("check")
	return c
}

func printLoginCommand(cmd, machine string) {
	where := "in a terminal on this machine"
	if machine != "" {
		where = "in your own terminal (the code is drawn there, and the step is marked done on " + machine + " when it succeeds)"
	}
	fmt.Fprintf(os.Stderr, "Run this %s:\n\n", where)
	fmt.Println(cmd)
}

// verifyRemoteLogin asks the machine whether it can run the step, so the
// user is not handed a command that fails there.
func verifyRemoteLogin(machine, key string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, loginSSHArgs(machine, false, loginScript(key, "--check"))...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimPrefix(strings.TrimSpace(stderr.String()), "error: ")
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(msg, "unknown command") || strings.Contains(msg, "unknown flag") {
			msg += " (hearth on " + machine + " is too old for this: hearth self-update there)"
		}
		return errors.New(msg)
	}
	return nil
}

// runLoginHere runs the step's command in the foreground, with the
// environment of its set (Hearth secrets filled in), then its check.
func runLoginHere(e *client.Engine, key string) error {
	p, set, err := e.InteractiveStep(key)
	if err != nil {
		return err
	}
	h, _ := os.UserHomeDir()
	env := append(proxyenv.Env(nil), "PATH="+filepath.Join(h, ".local", "bin")+":"+os.Getenv("PATH"))
	for k, v := range set {
		var ferr error
		v = secretRefRE.ReplaceAllStringFunc(v, func(m string) string {
			name := secretRefRE.FindStringSubmatch(m)[1]
			s, err := e.Secret(name)
			if err != nil {
				ferr = fmt.Errorf("secret %s for $%s: %w (hearth secret set %s)", name, k, err, name)
				return ""
			}
			return string(s.Value)
		})
		if ferr != nil {
			return ferr
		}
		env = append(env, k+"="+v)
	}
	sh := "bash"
	if _, err := exec.LookPath("bash"); err != nil {
		sh = "sh"
	}
	fmt.Fprintf(os.Stderr, "%s\n\n", firstNonEmpty(p.Do, p.Name))
	cmd := exec.Command(sh, "-lc", p.Commands[0])
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the login command failed: %v (run it again; nothing is marked done)", err)
	}
	if p.Check != "" {
		chk := exec.Command(sh, "-lc", p.Check)
		chk.Env = env
		if err := chk.Run(); err != nil {
			return fmt.Errorf("the command ended, but the step's check did not pass (%s): not marked done", p.Check)
		}
	}
	if err := e.MarkPlanDone(p.Key); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "\nDone: the step is marked done on this machine.")
	reportHealthQuiet(e) // the server's view of this machine (a few seconds)
	return nil
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}
