package logx

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	var out bytes.Buffer
	now := time.Unix(1_800_000_000, 0)
	l := NewLimiter(&out, 3, time.Minute, "TLS handshake error")
	l.Now = func() time.Time { return now }
	for i := 0; i < 100; i++ {
		fmt.Fprintf(l, "2026/10/23 12:00:%02d internal error: disk full writing 7/%d-0\n", i%60, i)
	}
	fmt.Fprintf(l, "2026/10/23 12:00:00 http: TLS handshake error from 1.2.3.4:5555: EOF\n")
	fmt.Fprintf(l, "2026/10/23 12:00:00 gc: expired=3\n")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 || !strings.Contains(lines[3], "suppressed for") || !strings.Contains(lines[4], "gc:") {
		t.Fatalf("%d lines:\n%s", len(lines), out.String())
	}
	out.Reset()
	now = now.Add(2 * time.Minute)
	fmt.Fprintf(l, "2026/10/23 12:02:00 internal error: disk full writing 7/1-0\n")
	if !strings.Contains(out.String(), "(97 similar messages suppressed)") || !strings.Contains(out.String(), "disk full") {
		t.Fatalf("after window:\n%s", out.String())
	}
}

func TestRotating(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.log")
	r, err := NewRotating(p, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n"
	for i := 0; i < 20; i++ {
		r.Write([]byte(line))
	}
	r.Close()
	for _, f := range []string{p, p + ".1", p + ".2"} {
		fi, err := os.Stat(f)
		if err != nil || fi.Size() > 100 {
			t.Fatalf("%s: %v %v", f, fi, err)
		}
	}
	if _, err := os.Stat(p + ".3"); err == nil {
		t.Fatal("kept more than 2 rotated files")
	}
}
