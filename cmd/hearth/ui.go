package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"golang.org/x/term"
	"golang.org/x/text/width"
)

// Terminal output helpers: tables aligned by display width (CJK titles
// take two columns each, which text/tabwriter does not know), short host
// names and relative times.

// dispWidth is the number of terminal columns s takes.
func dispWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func runeWidth(r rune) int {
	if r < 0x20 || (r >= 0x7f && r < 0xa0) || r == 0x200b {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
}

// truncW shortens s (whitespace collapsed) to at most w columns, ending in
// "…" when cut.
func truncW(s string, w int) string {
	s = strings.Join(strings.Fields(s), " ")
	if w <= 0 || dispWidth(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runeWidth(r)
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// table prints rows with columns padded by display width; the last column
// is not padded.
type table struct {
	rows [][]string
}

func (t *table) add(cols ...string) { t.rows = append(t.rows, cols) }

func (t *table) write(w io.Writer) {
	var widths []int
	for _, r := range t.rows {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			if n := dispWidth(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for _, r := range t.rows {
		var b strings.Builder
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-dispWidth(c)+2))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

func (t *table) print() { t.write(os.Stdout) }

// fit cuts the last column so every row fits the terminal width.
func (t *table) fit() {
	if len(t.rows) == 0 {
		return
	}
	n := len(t.rows[0])
	used := 0
	for c := 0; c < n-1; c++ {
		w := 0
		for _, r := range t.rows {
			if c < len(r) {
				if x := dispWidth(r[c]); x > w {
					w = x
				}
			}
		}
		used += w + 2
	}
	room := termWidth() - used - 1
	if room < 20 {
		room = 20
	}
	for _, r := range t.rows {
		if len(r) == n {
			r[n-1] = truncW(r[n-1], room)
		}
	}
}

// termWidth is the width of the terminal on stdout ($COLUMNS or 160 when
// not a terminal: piped output is read by agents, not cut to a screen).
func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		return w
	}
	if w, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && w > 20 {
		return w
	}
	return 160
}

func isTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && (term.IsTerminal(int(os.Stdout.Fd())) || quietOnTTY)
}

// quietOnTTY: setup sends step details to its log while the user is at a
// terminal; prompts (on stderr) still ask.
var quietOnTTY bool

// shortHost drops the domain of a host name (IP addresses stay whole).
func shortHost(h string) string {
	if net.ParseIP(h) != nil {
		return h
	}
	if i := strings.IndexByte(h, '.'); i > 0 {
		return h[:i]
	}
	return h
}

// agoUnix is a relative time ("3 minutes ago"), "-" for zero.
func agoUnix(unix int64) string {
	if unix <= 0 {
		return "-"
	}
	return humanize.Time(time.Unix(unix, 0))
}

// progressLine returns a callback that redraws one terminal line
// ("downloading 12 MiB / 22 MiB  54%"), at most every 100 ms, and clears
// it when done < 0.
func progressLine(label string) func(done, total int64) {
	var last time.Time
	return func(done, total int64) {
		if done < 0 {
			fmt.Print("\r\033[K")
			return
		}
		if time.Since(last) < 100*time.Millisecond {
			return
		}
		last = time.Now()
		if done > total {
			done = total
		}
		pct := 0
		if total > 0 {
			pct = int(100 * done / total)
		}
		fmt.Printf("\r\033[K  %s %s / %s  %d%%", label, humanize.IBytes(uint64(done)), humanize.IBytes(uint64(total)), pct)
	}
}
