package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hearth-project/hearth-cli/internal/xfer"
)

// What `hearth cp` prints. In a terminal: a header (source → target), a
// spinner while the two ends connect, one line saying how the data goes,
// a progress bar, and a ✓ line at the end. Elsewhere (logs, agents): plain
// lines, progress every 10 s. NO_COLOR turns the colours off.

var cpColor = cpTTY && os.Getenv("NO_COLOR") == ""

func paint(code, s string) string {
	if !cpColor {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func dim(s string) string   { return paint("2", s) }
func green(s string) string { return paint("32", s) }
func bold(s string) string  { return paint("1", s) }

// cpUI is one copy's output. Its methods may be called from the copy's
// goroutines; one mutex keeps the lines whole.
type cpUI struct {
	mu      sync.Mutex
	quiet   bool
	t0      time.Time
	spin    chan struct{} // closed when the connect spinner stops
	spinMsg string
	last    time.Time // last progress line written without a terminal
}

func newCPUI(quiet bool) *cpUI { return &cpUI{quiet: quiet, t0: time.Now()} }

// header names what is copied where: "hearth cp  gpu-a:/data/x → ~/Downloads/".
func (u *cpUI) header(src, dst string) {
	if u.quiet || !cpTTY {
		return
	}
	fmt.Fprintf(os.Stderr, "%s  %s %s %s\n", bold("hearth cp"), shortPath(src), dim("→"), shortPath(dst))
}

// connecting shows a spinner until the data connections are up.
func (u *cpUI) connecting(what string) {
	if u.quiet {
		return
	}
	if !cpTTY {
		fmt.Fprintf(os.Stderr, "hearth cp: %s\n", what)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.spin != nil {
		u.spinMsg = what
		return
	}
	u.spin, u.spinMsg = make(chan struct{}), what
	stop := u.spin
	go func() {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		t := time.NewTicker(90 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			u.mu.Lock()
			if u.spin == stop {
				fmt.Fprintf(os.Stderr, "\r\033[K  %s %s", paint("33", frames[i%len(frames)]), dim(u.spinMsg+" …"))
			}
			u.mu.Unlock()
		}
	}()
}

// stopSpin ends the spinner and clears its line (caller holds mu).
func (u *cpUI) stopSpin() {
	if u.spin != nil {
		close(u.spin)
		u.spin = nil
		if cpTTY {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
	}
}

// stop clears the spinner or the progress bar (an error follows, or done).
func (u *cpUI) stop() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stopSpin()
	if cpTTY && !u.quiet {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
}

// line prints one finished line (clears a spinner or progress bar first).
func (u *cpUI) line(label, text string) {
	if u.quiet {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stopSpin()
	if !cpTTY {
		fmt.Fprintf(os.Stderr, "hearth cp: %s %s\n", label, text)
		return
	}
	fmt.Fprintf(os.Stderr, "\r\033[K  %s %s\n", dim(fmt.Sprintf("%-7s", label)), text)
}

// info is xfer's Info (a shared disk, a fallback).
func (u *cpUI) info(f string, a ...any) { u.line("note", fmt.Sprintf(f, a...)) }

// route says how the data goes, in words.
func (u *cpUI) route(n int, via string, rtt time.Duration, compress bool) {
	var s string
	switch via {
	case "direct":
		s = fmt.Sprintf("direct, %d parallel connections, round trip %s", n, roundRTT(rtt))
	case "reverse":
		s = fmt.Sprintf("direct (the other machine dialled back), %d parallel connections, round trip %s", n, roundRTT(rtt))
	default:
		s = fmt.Sprintf("through SSH, %d connections %s", n, dim("(the two machines cannot connect directly)"))
	}
	if compress {
		s += ", zstd where it helps"
	}
	u.line("route", s)
}

func roundRTT(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return d.Round(10 * time.Microsecond).String()
	case d < 100*time.Millisecond:
		return d.Round(100 * time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}

// progress draws the bar (a terminal) or a line every 10 s.
func (u *cpUI) progress(done, total int64) {
	if u.quiet {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stopSpin()
	el := time.Since(u.t0).Seconds()
	rate := float64(done) / max(el, 0.001)
	pct := 100.0
	if total > 0 {
		pct = 100 * float64(done) / float64(total)
	}
	eta := ""
	if total > 0 && rate > 0 && done < total {
		eta = (time.Duration(float64(total-done) / rate * float64(time.Second))).Round(time.Second).String() + " left"
	}
	nums := fmt.Sprintf("%s / %s", bytesString(done), bytesString(total))
	speed := bytesString(int64(rate)) + "/s"
	if !cpTTY {
		if time.Since(u.last) < 10*time.Second && done < total {
			return
		}
		u.last = time.Now()
		fmt.Fprintf(os.Stderr, "hearth cp: %s  %.0f%%  %s  %s\n", nums, pct, speed, eta)
		return
	}
	const width = 28
	full := int(pct / 100 * width)
	full = min(max(full, 0), width)
	bar := paint("38;5;208", strings.Repeat("█", full)) + dim(strings.Repeat("░", width-full))
	tail := dim(" · " + speed)
	if eta != "" {
		tail += dim(" · " + eta)
	}
	fmt.Fprintf(os.Stderr, "\r\033[K  %s %3.0f%%  %s%s", bar, pct, nums, tail)
}

// done ends the copy: "✓ Copied 3 files, 1.2 GiB → gpu-a:/data/ckpt  4.1s · 300 MiB/s".
func (u *cpUI) done(r *xfer.Result, where string) {
	u.mu.Lock()
	u.stopSpin()
	if cpTTY && !u.quiet {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	u.mu.Unlock()
	p := r.Plan
	files := plural(len(p.Files), "file", "files")
	head := fmt.Sprintf("Copied %s, %s → %s", files, bytesString(r.Bytes), shortPath(where))
	var extra []string
	if p.Skipped > 0 {
		extra = append(extra, fmt.Sprintf("%d already up to date", p.Skipped))
	}
	if p.Resumed > 0 {
		extra = append(extra, fmt.Sprintf("%d resumed", p.Resumed))
	}
	if len(p.Kept) > 0 {
		extra = append(extra, fmt.Sprintf("%d kept (--no-clobber)", len(p.Kept)))
	}
	tail := []string{r.Took.Round(100 * time.Millisecond).String()}
	// A rate says little for a small copy: the time is connecting.
	if s := r.Took.Seconds(); s > 0 && r.Bytes >= 64<<20 {
		tail = append(tail, bytesString(int64(float64(r.Bytes)/s))+"/s")
	}
	switch r.Via {
	case "shared disk", "local":
		tail = append(tail, r.Via)
	case "direct", "reverse":
		tail = append(tail, fmt.Sprintf("%d direct connections", r.Streams))
	default:
		tail = append(tail, "through SSH")
	}
	if len(extra) > 0 {
		head += " (" + strings.Join(extra, ", ") + ")"
	}
	if cpTTY {
		fmt.Printf("%s %s  %s\n", green("✓"), head, dim(strings.Join(tail, " · ")))
		return
	}
	fmt.Printf("%s  (%s)\n", head, strings.Join(tail, ", "))
}

// shortPath writes the home directory as ~.
func shortPath(p string) string {
	h, _ := os.UserHomeDir()
	machine, path, remote := strings.Cut(p, ":")
	if remote && !strings.HasPrefix(machine, "/") {
		return machine + ":" + path // the other machine's home is not ours
	}
	if h != "" {
		if rest, ok := strings.CutPrefix(p, h+string(filepath.Separator)); ok {
			return "~/" + rest
		}
		if p == h {
			return "~"
		}
	}
	return p
}
