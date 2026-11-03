package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/proxyenv"
	"github.com/hearth-project/hearth-cli/internal/ttyrun"
)

// Interactive restore steps (a login that shows a QR code) run under a
// pseudo-terminal in the background on the machine; the agent reads the
// code as a PNG and shows it in the chat, the user scans it, Hearth runs
// the step's check and marks it done. From another machine every command
// goes over `hearth ssh` (--on <machine>). Only plan steps that are manual,
// tty = true and name their command (run) can be started.

// secretRefRE: a Hearth secret in a step's environment, ${secret:NAME}.
var secretRefRE = regexp.MustCompile(`\$\{secret:([A-Za-z0-9][A-Za-z0-9._-]{0,127})\}`)

// ttyReport is what the tty commands print with --json.
type ttyReport struct {
	ID      string   `json:"session_id"`
	Machine string   `json:"machine,omitempty"`
	Key     string   `json:"key"`
	Name    string   `json:"name,omitempty"`
	Status  string   `json:"status"` // starting, waiting_for_scan, running, succeeded, failed, stopped, expired
	QRHash  string   `json:"qr_hash,omitempty"`
	QRPNG   string   `json:"qr_png,omitempty"`  // base64, over ssh only (--png)
	QRPath  string   `json:"qr_path,omitempty"` // the PNG on this machine (temporary, 0600)
	URLs    []string `json:"urls,omitempty"`
	Tail    []string `json:"output_tail,omitempty"`
	Exit    *int     `json:"exit_code,omitempty"`
	Check   string   `json:"check,omitempty"`
	Error   string   `json:"error,omitempty"`
	Next    string   `json:"next,omitempty"`
}

func (r *ttyReport) final() bool {
	switch r.Status {
	case "succeeded", "failed", "stopped", "expired":
		return true
	}
	return false
}

func ttyView(home, id string, withPNG bool) (*ttyReport, error) {
	v, err := ttyrun.Read(home, id)
	if err != nil {
		return nil, err
	}
	r := &ttyReport{ID: id, Key: v.Key, Name: v.Name, Status: v.Status, URLs: v.URLs, Tail: v.Tail, Exit: v.Exit, Check: v.Check, Error: v.Error}
	if v.Status == "running" && v.Waiting && v.QR != nil {
		r.Status, r.QRHash = "waiting_for_scan", v.QR.Hash
		if withPNG {
			r.QRPNG = base64.StdEncoding.EncodeToString(v.QR.PNG(8))
		}
	}
	return r, nil
}

// ttyWait polls the session until something an agent acts on happens: it
// ends, a (new) code appears, or wait runs out.
func ttyWait(home, id string, wait time.Duration, sinceHash string, withPNG bool) (*ttyReport, error) {
	deadline := time.Now().Add(wait)
	for {
		r, err := ttyView(home, id, withPNG)
		if err != nil {
			return nil, err
		}
		if r.final() || (r.QRHash != "" && r.QRHash != sinceHash) || time.Now().After(deadline) {
			return r, nil
		}
		time.Sleep(400 * time.Millisecond)
	}
}

// qrDir holds the codes shown to the user: this machine only, removed when
// the step ends (a login code is a one-time credential).
func qrDir() string { return filepath.Join(os.TempDir(), fmt.Sprintf("hearth-qr-%d", os.Getuid())) }

// keepQR writes the code of r to a local file (r.QRPath) and drops the
// base64 copy; a finished session's files are removed.
func keepQR(r *ttyReport, out string) {
	if r.final() {
		matches, _ := filepath.Glob(filepath.Join(qrDir(), r.ID+"-*.png"))
		for _, m := range matches {
			os.Remove(m)
		}
	}
	if r.QRPNG == "" {
		return
	}
	b, err := base64.StdEncoding.DecodeString(r.QRPNG)
	r.QRPNG = ""
	if err != nil {
		return
	}
	p := out
	if p == "" {
		_ = os.MkdirAll(qrDir(), 0o700)
		p = filepath.Join(qrDir(), r.ID+"-"+r.QRHash+".png")
	}
	if os.WriteFile(p, b, 0o600) == nil {
		r.QRPath = p
	}
}

func (r *ttyReport) setNext() {
	switch r.Status {
	case "waiting_for_scan":
		r.Next = "show qr_path to the user as an image and ask them to scan it; then poll until the status changes (a new code replaces an expired one)"
	case "running", "starting":
		r.Next = "poll again; if output_tail asks a question, answer with input"
	case "succeeded":
		r.Next = "the step's check passed and it is marked done"
	case "failed":
		r.Next = "read output_tail and error; fix the cause or start the step again"
	}
}

// remoteTTY runs `hearth recipe tty <args>` on another machine over hearth ssh.
func remoteTTY(machine string, args ...string) (*ttyReport, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	bin := "hearth"
	if b := os.Getenv("HEARTH_TTY_REMOTE_BIN"); b != "" {
		bin = shellQuote(b) // a test build on the target
	}
	remote := `PATH="$HOME/.local/bin:$PATH"; exec ` + bin + ` recipe tty ` + strings.Join(q, " ") + ` --json --png`
	cmd := exec.Command(self, "ssh", machine, "--", "sh", "-c", shellQuote(remote))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimPrefix(strings.TrimSpace(stderr.String()), "error: ")
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(msg, "unknown command \"tty\"") || strings.Contains(msg, "unknown flag") {
			msg += " (hearth on " + machine + " is too old for interactive steps: hearth self-update there)"
		}
		return nil, errors.New(msg)
	}
	var r ttyReport
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("%s: unexpected answer: %v", machine, err)
	}
	r.Machine = machine
	return &r, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

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

func printTTY(r *ttyReport, asJSON bool) error {
	r.setNext()
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	where := ""
	if r.Machine != "" {
		where = " on " + r.Machine
	}
	fmt.Printf("session %s%s: %s\n", r.ID, where, r.Status)
	if r.QRPath != "" {
		fmt.Println("scan the code:", r.QRPath)
	}
	for _, u := range r.URLs {
		fmt.Println("link:", u)
	}
	if len(r.Tail) > 0 {
		fmt.Println("output:\n  " + strings.Join(r.Tail, "\n  "))
	}
	if r.Error != "" {
		fmt.Println("error:", r.Error)
	}
	if r.Status == "failed" {
		return errors.New("the step failed")
	}
	return nil
}

func ttyCmd() *cobra.Command {
	var on, qrOut, since string
	var asJSON, png bool
	var wait time.Duration
	c := &cobra.Command{Use: "tty", Short: "Interactive restore steps (QR-code logins) in the background, for agents: start, status, input, stop",
		Long: "Runs a plan step that needs a terminal (manual, tty = true, with run) under a pseudo-terminal in the\n" +
			"background on this machine or another (--on <machine>, over hearth ssh). status returns the QR code it\n" +
			"shows as a PNG file; once the command ends, its check runs and the step is marked done.\n\n" +
			"  hearth recipe tty start <key> --on <machine>        start it; waits for a code (--wait)\n" +
			"  hearth recipe tty status <session> --on <machine>   state, code, output (a new code: --since <qr_hash>)\n" +
			"  hearth recipe tty input <session> <text> --on ...   answer a prompt\n" +
			"  hearth recipe tty stop <session> --on <machine>"}
	flags := func(x *cobra.Command) {
		x.Flags().StringVar(&on, "on", "", "the machine (default: this one)")
		x.Flags().BoolVar(&asJSON, "json", false, "machine-readable")
		x.Flags().BoolVar(&png, "png", false, "include the code as base64 PNG (used over ssh)")
		_ = x.Flags().MarkHidden("png")
	}
	waitFlags := func(x *cobra.Command) {
		x.Flags().DurationVar(&wait, "wait", 20*time.Second, "wait this long for a code or the end")
		x.Flags().StringVar(&qrOut, "qr-out", "", "write the code to this PNG file (default: a temporary file, removed when the step ends)")
	}
	finish := func(r *ttyReport) error {
		if !png {
			keepQR(r, qrOut)
		}
		return printTTY(r, asJSON)
	}
	start := &cobra.Command{Use: "start <step-key>", Short: "Start an interactive step in the background", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		if !isThisMachine(e, on) {
			r, err := remoteTTY(on, "start", a[0], "--wait", wait.String())
			if err != nil {
				return err
			}
			return finish(r)
		}
		p, secEnv, err := e.InteractiveStep(a[0])
		if err != nil {
			return err
		}
		id, err := ttyrun.Create(e.Home, ttyrun.Spec{Key: p.Key, Name: p.Name, Run: p.Commands[0], Check: p.Check, Env: secEnv})
		if err != nil {
			return err
		}
		self, err := os.Executable()
		if err != nil {
			return err
		}
		null, _ := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		bg := exec.Command(self, "recipe", "tty", "serve", id)
		bg.Env = append(os.Environ(), "HEARTH_HOME="+e.Home)
		bg.Stdin, bg.Stdout, bg.Stderr = null, null, null
		bg.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := bg.Start(); err != nil {
			return err
		}
		_ = bg.Process.Release()
		r, err := ttyWait(e.Home, id, wait, "", true)
		if err != nil {
			return err
		}
		return finish(r)
	}}
	status := &cobra.Command{Use: "status <session>", Short: "A session's state, its QR code and output", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		if !isThisMachine(e, on) {
			args := []string{"status", a[0], "--wait", wait.String()}
			if since != "" {
				args = append(args, "--since", since)
			}
			r, err := remoteTTY(on, args...)
			if err != nil {
				return err
			}
			return finish(r)
		}
		r, err := ttyWait(e.Home, a[0], wait, since, true)
		if err != nil {
			return err
		}
		return finish(r)
	}}
	status.Flags().StringVar(&since, "since", "", "wait for a code other than this qr_hash (or the end)")
	input := &cobra.Command{Use: "input <session> <text>", Short: "Type an answer into the step's terminal (Enter added)", Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, a []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		if !isThisMachine(e, on) {
			r, err := remoteTTY(on, "input", a[0], a[1])
			if err != nil {
				return err
			}
			return finish(r)
		}
		if err := ttyrun.Input(e.Home, a[0], a[1]); err != nil {
			return err
		}
		time.Sleep(time.Second)
		r, err := ttyView(e.Home, a[0], true)
		if err != nil {
			return err
		}
		return finish(r)
	}}
	stop := &cobra.Command{Use: "stop <session>", Short: "End a running interactive step", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		if !isThisMachine(e, on) {
			r, err := remoteTTY(on, "stop", a[0])
			if err != nil {
				return err
			}
			return finish(r)
		}
		if err := ttyrun.Stop(e.Home, a[0]); err != nil {
			return err
		}
		var r *ttyReport
		for end := time.Now().Add(8 * time.Second); ; time.Sleep(300 * time.Millisecond) {
			if r, err = ttyView(e.Home, a[0], false); err != nil || r.final() || time.Now().After(end) {
				break
			}
		}
		if err != nil {
			return err
		}
		return finish(r)
	}}
	serve := &cobra.Command{Use: "serve <session>", Hidden: true, Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		spec, err := ttyrun.LoadSpec(e.Home, a[0])
		if err != nil {
			return ttyrun.Fail(e.Home, a[0], err)
		}
		h, _ := os.UserHomeDir()
		env := proxyenv.Env(nil)
		env = append(env, "PATH="+filepath.Join(h, ".local", "bin")+":"+os.Getenv("PATH"))
		var secrets []string
		for k, v := range spec.Env {
			var ferr error
			v = secretRefRE.ReplaceAllStringFunc(v, func(m string) string {
				name := secretRefRE.FindStringSubmatch(m)[1]
				s, err := e.Secret(name)
				if err != nil {
					ferr = fmt.Errorf("secret %s for $%s: %w (hearth secret set %s)", name, k, err, name)
					return ""
				}
				secrets = append(secrets, string(s.Value))
				return string(s.Value)
			})
			if ferr != nil {
				return ttyrun.Fail(e.Home, a[0], ferr)
			}
			env = append(env, k+"="+v)
		}
		return ttyrun.Serve(e.Home, a[0], env, secrets, func() {
			_ = e.MarkPlanDone(spec.Key)
			reportHealthQuiet(e)
		})
	}}
	for _, x := range []*cobra.Command{start, status, input, stop} {
		flags(x)
	}
	waitFlags(start)
	waitFlags(status)
	c.AddCommand(start, status, input, stop, serve)
	return c
}
