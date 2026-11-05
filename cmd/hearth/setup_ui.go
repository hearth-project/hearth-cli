package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// setupOut keeps `hearth setup` to one short line per topic. The details
// every step prints (each restored file, skill and recipe step) go to a
// log file instead; HEARTH_VERBOSE=1 prints them as before. Prompts read
// on stderr and stay visible.
type setupOut struct {
	real    *os.File
	log     *os.File
	logPath string
	verbose bool
	warns   int
	// results wait for finish; working is the running "Setting up ..."
	// line, interrupted when a question came in between.
	results     [][2]string
	working     string
	interrupted bool
}

// activeSetup is the running setup's output, for questions asked deep
// inside its steps (askSetup).
var activeSetup *setupOut

// askSetup asks like setupOut.ask, also outside setup.
func askSetup(question string, about, choices []string, def string) (string, bool) {
	o := activeSetup
	if o == nil {
		o = &setupOut{real: os.Stdout}
	}
	return o.ask(question, about, choices, def)
}

func newSetupOut() *setupOut {
	o := &setupOut{real: os.Stdout, verbose: os.Getenv("HEARTH_VERBOSE") == "1"}
	activeSetup = o
	if o.verbose {
		return o
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "hearth")
	if os.MkdirAll(dir, 0o700) == nil {
		o.logPath = filepath.Join(dir, "setup-"+time.Now().Format("20060102-150405")+".log")
		if f, err := os.OpenFile(o.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			o.log = f
		}
	}
	return o
}

// quiet runs fn with its output in the log.
func (o *setupOut) quiet(fn func()) {
	if o.log == nil {
		fn()
		return
	}
	wasTTY := isTTY()
	os.Stdout = o.log
	quietOnTTY = wasTTY
	defer func() { os.Stdout, quietOnTTY = o.real, false }()
	fn()
}

// fact prints "  label       text" at once: what setup found before it
// asks anything (account, login, network).
func (o *setupOut) fact(label, text string) {
	text = strings.ReplaceAll(text, "\n", "\n"+strings.Repeat(" ", labelWidth+4))
	fmt.Fprintf(o.real, "  %-*s  %s\n", labelWidth, label, text)
	if o.log != nil {
		fmt.Fprintf(o.log, "== %s: %s\n", label, text)
	}
}

const labelWidth = 10

// line records one result of setup ("label  text"); the results are
// printed together under "Setting up this machine ... done" (finish).
func (o *setupOut) line(label, text string) {
	o.results = append(o.results, [2]string{label, text})
	if o.log != nil {
		fmt.Fprintf(o.log, "== %s: %s\n", label, text)
	}
}

// warn reports something that went wrong but does not stop setup.
func (o *setupOut) warn(label string, err error) {
	o.warns++
	o.line(label, "⚠ "+err.Error())
}

// title opens setup: a welcome line and what this machine is.
func (o *setupOut) title(s string) { fmt.Fprintln(o.real, s) }

// start says setup is working ("Setting up this machine ..."); finish
// ends the line with "done" and prints the results.
func (o *setupOut) start(what string) {
	o.working = what
	fmt.Fprintf(o.real, "\n%s ...", what)
}

func (o *setupOut) finish() {
	switch {
	case o.working == "":
	case o.interrupted:
		fmt.Fprintf(o.real, "\n%s ... done\n", o.working)
	default:
		fmt.Fprintln(o.real, " done")
	}
	o.working, o.interrupted = "", false
	o.printResults()
}

func (o *setupOut) printResults() {
	if len(o.results) == 0 {
		return
	}
	fmt.Fprintln(o.real)
	for _, r := range o.results {
		text := strings.ReplaceAll(r[1], "\n", "\n"+strings.Repeat(" ", labelWidth+4))
		fmt.Fprintf(o.real, "  %-*s  %s\n", labelWidth, r[0], text)
	}
	o.results = nil
}

// ask asks one question the way installers do: the question, a line or
// two on what it is for, the choices, then the default in brackets:
//
//	[/root/work] >>>
//
// It returns the answer ("" = Enter) and false without a terminal answer.
func (o *setupOut) ask(question string, about []string, choices []string, def string) (string, bool) {
	if o.working != "" && !o.interrupted {
		o.interrupted = true
		fmt.Fprintln(o.real)
	}
	var b strings.Builder
	b.WriteString("\n" + question + "\n")
	for _, l := range about {
		b.WriteString(l + "\n")
	}
	if len(choices) > 0 {
		b.WriteString("\n")
		for _, c := range choices {
			b.WriteString("  - " + c + "\n")
		}
	}
	fmt.Fprintf(&b, "\n[%s] >>> ", def)
	a, ok := askLine(b.String())
	if o.log != nil {
		fmt.Fprintf(o.log, "?? %s -> %q\n", question, a)
	}
	return a, ok
}

func (o *setupOut) close() {
	o.printResults() // setup stopped early: what it did so far
	if o.log != nil {
		o.log.Close()
	}
}

// logHint names the log, when there is one.
func (o *setupOut) logHint() string {
	if o.logPath == "" {
		return ""
	}
	h, _ := os.UserHomeDir()
	p := o.logPath
	if rest, ok := strings.CutPrefix(p, h+string(filepath.Separator)); ok {
		p = "~/" + rest
	}
	return "details: " + p
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
