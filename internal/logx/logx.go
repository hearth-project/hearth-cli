// Package logx keeps long-running server logs small: a size-rotated file
// writer and a limiter that collapses floods of similar lines (a broken disk
// failing every request, scanners hitting a public port).
package logx

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Rotating is an io.Writer that keeps path below Max bytes, moving full files
// to path.1 ... path.<Keep>.
type Rotating struct {
	Path string
	Max  int64
	Keep int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// NewRotating opens (appends to) path.
func NewRotating(path string, max int64, keep int) (*Rotating, error) {
	r := &Rotating{Path: path, Max: max, Keep: keep}
	return r, r.open()
}

func (r *Rotating) open() error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(r.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, fi.Size()
	return nil
}

func (r *Rotating) rotate() error {
	r.f.Close()
	for i := r.Keep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", r.Path, i), fmt.Sprintf("%s.%d", r.Path, i+1))
	}
	if r.Keep > 0 {
		os.Rename(r.Path, r.Path+".1")
	} else {
		os.Remove(r.Path)
	}
	return r.open()
}

func (r *Rotating) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Max > 0 && r.size+int64(len(p)) > r.Max && r.size > 0 {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// Close closes the current file.
func (r *Rotating) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

// Limiter passes at most Burst lines per Window for each kind of line
// (the text with digits and addresses stripped) and reports how many were
// dropped when that kind shows up again. Lines containing any of Drop are
// discarded (noise such as TLS handshake errors from scanners).
type Limiter struct {
	Out    io.Writer
	Burst  int
	Window time.Duration
	Drop   []string
	Now    func() time.Time

	mu    sync.Mutex
	kinds map[string]*kind
}

type kind struct {
	start      time.Time
	n, dropped int
}

// NewLimiter allows burst similar lines per window.
func NewLimiter(out io.Writer, burst int, window time.Duration, drop ...string) *Limiter {
	return &Limiter{Out: out, Burst: burst, Window: window, Drop: drop, Now: time.Now, kinds: map[string]*kind{}}
}

// kindOf normalises a log line: timestamps, numbers, hex ids and ports vary
// between otherwise identical messages.
func kindOf(line []byte) string {
	var b strings.Builder
	digits := false
	for _, c := range line {
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' && digits {
			digits = true
			continue
		}
		if digits {
			b.WriteByte('#')
			digits = false
		}
		b.WriteByte(c)
		if b.Len() > 120 {
			break
		}
	}
	return b.String()
}

func (l *Limiter) Write(p []byte) (int, error) {
	for _, d := range l.Drop {
		if bytes.Contains(p, []byte(d)) {
			return len(p), nil
		}
	}
	now := l.Now()
	k := kindOf(p)
	l.mu.Lock()
	e := l.kinds[k]
	if e == nil || now.Sub(e.start) > l.Window {
		dropped := 0
		if e != nil {
			dropped = e.dropped
		}
		e = &kind{start: now}
		l.kinds[k] = e
		if len(l.kinds) > 10000 { // bound memory: forget everything
			l.kinds = map[string]*kind{k: e}
		}
		if dropped > 0 {
			l.mu.Unlock()
			fmt.Fprintf(l.Out, "%s (%d similar messages suppressed)\n", now.Format("2006/01/02 15:04:05"), dropped)
			l.mu.Lock()
		}
	}
	e.n++
	if e.n > l.Burst {
		e.dropped++
		l.mu.Unlock()
		return len(p), nil
	}
	last := e.n == l.Burst
	l.mu.Unlock()
	if _, err := l.Out.Write(p); err != nil {
		return 0, err
	}
	if last {
		fmt.Fprintf(l.Out, "%s (further similar messages are suppressed for %s)\n", now.Format("2006/01/02 15:04:05"), l.Window)
	}
	return len(p), nil
}
