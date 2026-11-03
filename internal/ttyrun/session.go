package ttyrun

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// A session lives in <home>/tty/<id>: spec.json (what runs, without secret
// values), state.json, out.log (the terminal output, secret values already
// replaced; removed when the step ends, since a login code is a one-time
// credential) and the fifo "in" for answers to prompts.

const (
	Cols, Rows = 200, 60
	// MaxRun ends a step that still waits after this long (a code expires
	// sooner; programs print a new one meanwhile).
	MaxRun = 20 * time.Minute
	keep   = 2 * time.Hour // finished sessions are removed after this
)

var idRE = regexp.MustCompile(`^[0-9a-f]{12}$`)

// Spec is the step a session runs.
type Spec struct {
	Key   string `json:"key"`
	Name  string `json:"name,omitempty"`
	Run   string `json:"run"`
	Check string `json:"check,omitempty"`
	// Env: variables for the command; "${secret:NAME}" in a value is a
	// Hearth secret, fetched by the background process (never written here).
	Env map[string]string `json:"env,omitempty"`
}

// State is what the background process reports.
type State struct {
	ID      string   `json:"id"`
	Key     string   `json:"key"`
	Name    string   `json:"name,omitempty"`
	Status  string   `json:"status"` // running, succeeded, failed, stopped, expired
	PID     int      `json:"pid,omitempty"`
	Started int64    `json:"started"`
	Ended   int64    `json:"ended,omitempty"`
	Exit    *int     `json:"exit_code,omitempty"`
	Check   string   `json:"check,omitempty"` // passed, failed, "" = none
	Error   string   `json:"error,omitempty"`
	Tail    []string `json:"tail,omitempty"` // the last lines, when it ended
}

func Root(home string) string { return filepath.Join(home, "tty") }

func dir(home, id string) (string, error) {
	if !idRE.MatchString(id) {
		return "", errors.New("invalid session id")
	}
	return filepath.Join(Root(home), id), nil
}

// Create makes a session directory for spec and returns its id. The caller
// then starts the background process (`hearth recipe tty serve <id>`).
func Create(home string, spec Spec) (string, error) {
	cleanup(home)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	d := filepath.Join(Root(home), id)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(Root(home), 0o700)
	if err := writeJSON(filepath.Join(d, "spec.json"), spec); err != nil {
		return "", err
	}
	if err := writeJSON(filepath.Join(d, "state.json"), State{ID: id, Key: spec.Key, Name: spec.Name, Status: "starting", Started: time.Now().Unix()}); err != nil {
		return "", err
	}
	return id, nil
}

// LoadSpec reads a session's spec (for the background process).
func LoadSpec(home, id string) (Spec, error) {
	var s Spec
	d, err := dir(home, id)
	if err != nil {
		return s, err
	}
	b, err := os.ReadFile(filepath.Join(d, "spec.json"))
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

func cleanup(home string) {
	es, _ := os.ReadDir(Root(home))
	for _, e := range es {
		d := filepath.Join(Root(home), e.Name())
		st, err := readState(d)
		if err != nil {
			if fi, err := os.Stat(d); err == nil && time.Since(fi.ModTime()) > keep {
				os.RemoveAll(d)
			}
			continue
		}
		if st.Ended > 0 && time.Since(time.Unix(st.Ended, 0)) > keep {
			os.RemoveAll(d)
		}
	}
}

func writeJSON(p string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func readState(d string) (State, error) {
	var st State
	b, err := os.ReadFile(filepath.Join(d, "state.json"))
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

// Serve runs the session's step under a pseudo-terminal until it ends (the
// background process). env is the step's environment, secrets the values to
// keep out of the output. check runs after the command; done is called when
// it passed.
func Serve(home, id string, env []string, secrets []string, done func()) error {
	d, err := dir(home, id)
	if err != nil {
		return err
	}
	spec, err := LoadSpec(home, id)
	if err != nil {
		return err
	}
	st, _ := readState(d)
	st.ID, st.Key, st.Name, st.PID, st.Status = id, spec.Key, spec.Name, os.Getpid(), "running"
	finish := func(status string) error {
		st.Status, st.Ended = status, time.Now().Unix()
		return writeJSON(filepath.Join(d, "state.json"), st)
	}
	fifo := filepath.Join(d, "in")
	_ = syscall.Mkfifo(fifo, 0o600)
	in, _ := os.OpenFile(fifo, os.O_RDWR, 0) // RDWR: never blocks, never sees EOF
	logf, err := os.OpenFile(filepath.Join(d, "out.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		st.Error = err.Error()
		return finish("failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), MaxRun)
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR1, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)
	go func() { <-sig; _ = os.WriteFile(filepath.Join(d, "stop"), nil, 0o600); cancel() }()
	sh := "bash"
	if _, err := exec.LookPath("bash"); err != nil {
		sh = "sh"
	}
	cmd := exec.CommandContext(ctx, sh, "-lc", spec.Run)
	cmd.Env = append(env, "TERM=xterm-256color", fmt.Sprintf("COLUMNS=%d", Cols), fmt.Sprintf("LINES=%d", Rows))
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	master, err := startPTY(cmd, Cols, Rows)
	if err != nil {
		st.Error = err.Error()
		logf.Close()
		return finish("failed")
	}
	_ = writeJSON(filepath.Join(d, "state.json"), st)
	if in != nil {
		go io.Copy(master, in)
	}
	red := newRedactor(logf, secrets)
	copied := make(chan struct{})
	go func() { io.Copy(red, master); close(copied) }()
	werr := cmd.Wait()
	select {
	case <-copied:
	case <-time.After(2 * time.Second):
	}
	red.Flush()
	master.Close()
	logf.Close()
	code := cmd.ProcessState.ExitCode()
	st.Exit = &code
	status := ""
	switch {
	case fileExists(filepath.Join(d, "stop")):
		status = "stopped"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		status = "expired"
	}
	// The output's last lines, without the login code, stay in the state.
	if b, err := os.ReadFile(filepath.Join(d, "out.log")); err == nil {
		s := NewScreen(Cols, Rows)
		s.Write(b)
		st.Tail = Tail(s, 20)
	}
	os.Remove(filepath.Join(d, "out.log"))
	os.Remove(fifo)
	if status != "" {
		return finish(status)
	}
	ok := werr == nil
	if spec.Check != "" {
		cctx, ccancel := context.WithTimeout(context.Background(), time.Minute)
		c := exec.CommandContext(cctx, sh, "-lc", spec.Check)
		c.Env = env
		err := c.Run()
		ccancel()
		ok = err == nil
		st.Check = map[bool]string{true: "passed", false: "failed"}[ok]
	}
	if ok {
		if done != nil {
			done()
		}
		return finish("succeeded")
	}
	return finish("failed")
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// View is a session as an agent sees it.
type View struct {
	State
	Waiting bool     `json:"-"` // a login code is on the screen
	QR      *QR      `json:"-"`
	URLs    []string `json:"urls,omitempty"`
}

// Read reports a session: its state and, while it runs, the screen.
func Read(home, id string) (*View, error) {
	d, err := dir(home, id)
	if err != nil {
		return nil, err
	}
	st, err := readState(d)
	if err != nil {
		return nil, fmt.Errorf("no such session %s (finished sessions are removed after 2 hours)", id)
	}
	v := &View{State: st}
	if st.Status == "running" && st.PID > 0 && syscall.Kill(st.PID, 0) != nil {
		v.Status, v.Error = "failed", "the background process ended without a result"
	}
	if st.Ended != 0 {
		return v, nil
	}
	b, err := os.ReadFile(filepath.Join(d, "out.log"))
	if err != nil {
		return v, nil
	}
	s := NewScreen(Cols, Rows)
	s.Write(b)
	v.Tail = Tail(s, 20)
	v.URLs = URLs(textOutsideQR(s))
	if q := s.FindQR(); q != nil {
		// A code counts as waiting while little was printed after it.
		after := 0
		lines := s.lines()
		for _, row := range lines[q.Rows[1]+1:] {
			if strings.TrimSpace(rowText(row)) != "" {
				after++
			}
		}
		if after <= 8 {
			v.QR, v.Waiting = q, true
		}
	}
	return v, nil
}

// Tail is the last n non-empty lines outside QR codes.
func Tail(s *Screen, n int) []string {
	lines := textOutsideQR(s)
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func textOutsideQR(s *Screen) []string {
	var out []string
	for _, row := range s.lines() {
		if !qrRow(row) {
			out = append(out, rowText(row))
		}
	}
	return out
}

// Input types text into the step's terminal (Enter is added).
func Input(home, id, text string) error {
	d, err := dir(home, id)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(d, "in"), os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("the step is not running")
	}
	defer f.Close()
	_, err = f.Write([]byte(text + "\r"))
	return err
}

// Stop ends a running step.
func Stop(home, id string) error {
	d, err := dir(home, id)
	if err != nil {
		return err
	}
	st, err := readState(d)
	if err != nil {
		return err
	}
	if st.Ended != 0 {
		return nil
	}
	_ = os.WriteFile(filepath.Join(d, "stop"), nil, 0o600)
	if st.PID > 0 {
		// The step runs in its own session under the background process.
		_ = syscall.Kill(st.PID, syscall.SIGUSR1)
	}
	return nil
}

// redactor replaces secret values in a stream; it holds back a tail that
// could be the start of one.
type redactor struct {
	w       io.Writer
	secrets [][]byte
	buf     []byte
}

func newRedactor(w io.Writer, secrets []string) *redactor {
	r := &redactor{w: w}
	for _, s := range secrets {
		if len(s) >= 4 {
			r.secrets = append(r.secrets, []byte(s))
		}
	}
	return r
}

func (r *redactor) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	for _, s := range r.secrets {
		r.buf = bytes.ReplaceAll(r.buf, s, []byte("********"))
	}
	// Hold back only a tail that could be the start of a secret.
	keep := 0
	for _, s := range r.secrets {
		for k := min(len(s)-1, len(r.buf)); k > keep; k-- {
			if bytes.HasSuffix(r.buf, s[:k]) {
				keep = k
				break
			}
		}
	}
	if n := len(r.buf) - keep; n > 0 {
		if _, err := r.w.Write(r.buf[:n]); err != nil {
			return 0, err
		}
		r.buf = append(r.buf[:0], r.buf[n:]...)
	}
	return len(p), nil
}

func (r *redactor) Flush() {
	if len(r.buf) > 0 {
		r.w.Write(r.buf)
		r.buf = nil
	}
}

// Fail ends a session that could not start (the background process could
// not prepare it).
func Fail(home, id string, cause error) error {
	d, err := dir(home, id)
	if err != nil {
		return err
	}
	st, _ := readState(d)
	st.Status, st.Ended, st.Error = "failed", time.Now().Unix(), cause.Error()
	return writeJSON(filepath.Join(d, "state.json"), st)
}
