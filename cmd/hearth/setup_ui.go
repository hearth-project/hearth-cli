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
}

func newSetupOut() *setupOut {
	o := &setupOut{real: os.Stdout, verbose: os.Getenv("HEARTH_VERBOSE") == "1"}
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

// line prints "  label  text" (continuation lines indented under text).
func (o *setupOut) line(label, text string) {
	text = strings.ReplaceAll(text, "\n", "\n"+strings.Repeat(" ", 15))
	fmt.Fprintf(o.real, "  %-12s %s\n", label, text)
	if o.log != nil {
		fmt.Fprintf(o.log, "== %s: %s\n", label, text)
	}
}

// warn reports something that went wrong but does not stop setup.
func (o *setupOut) warn(label string, err error) {
	o.warns++
	o.line(label, "⚠ "+err.Error())
}

func (o *setupOut) title(s string) { fmt.Fprintln(o.real, s) }

func (o *setupOut) close() {
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
