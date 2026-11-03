package ttyrun

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A login-like step: refuses to run without a terminal, prints a QR code,
// waits for an answer, then the check passes. The secret value never shows.
func TestServe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pseudo-terminals: linux")
	}
	home := t.TempDir()
	qr, _ := filepath.Abs("testdata/ansi-bg.txt")
	done := filepath.Join(home, "logged-in")
	run := `[ -t 0 ] || { echo "needs an interactive terminal"; exit 3; }; echo "key is $THE_KEY"; cat ` + qr + `; read answer; echo "got $answer"; [ "$answer" = yes ] && touch ` + done
	id, err := Create(home, Spec{Key: "learned/abc", Name: "login", Run: run, Check: "test -f " + done})
	if err != nil {
		t.Fatal(err)
	}
	marked := make(chan bool, 1)
	go func() {
		env := append(os.Environ(), "THE_KEY=s3cr3t-value")
		if err := Serve(home, id, env, []string{"s3cr3t-value"}, func() { marked <- true }); err != nil {
			t.Error(err)
		}
	}()
	var v *View
	for i := 0; i < 100; i++ {
		if v, err = Read(home, id); err == nil && v.Waiting {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if v == nil || !v.Waiting || v.QR == nil || len(v.QR.Modules) != 37 {
		t.Fatalf("no code: %+v", v)
	}
	if strings.Contains(strings.Join(v.Tail, "\n"), "s3cr3t") || !strings.Contains(strings.Join(v.Tail, "\n"), "key is ********") {
		t.Fatalf("secret in the output: %q", v.Tail)
	}
	if len(v.URLs) != 1 {
		t.Fatalf("urls %v", v.URLs)
	}
	if err := Input(home, id, "yes"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-marked:
	case <-time.After(10 * time.Second):
		t.Fatal("not marked done")
	}
	for i := 0; i < 50; i++ {
		if v, _ = Read(home, id); v.Ended != 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if v.Status != "succeeded" || v.Check != "passed" || v.QR != nil {
		t.Fatalf("end: %+v", v.State)
	}
	if _, err := os.Stat(filepath.Join(Root(home), id, "out.log")); !os.IsNotExist(err) {
		t.Fatal("the output (with the code) was kept")
	}

	// Stopped while waiting.
	id2, _ := Create(home, Spec{Key: "k", Run: "sleep 60"})
	go Serve(home, id2, os.Environ(), nil, nil)
	for i := 0; i < 50; i++ {
		if v, _ = Read(home, id2); v.Status == "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := Stop(home, id2); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if v, _ = Read(home, id2); v.Ended != 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if v.Status != "stopped" {
		t.Fatalf("stop: %+v", v.State)
	}
	if _, err := Read(home, "../../etc"); err == nil {
		t.Fatal("accepted a path as session id")
	}
}
