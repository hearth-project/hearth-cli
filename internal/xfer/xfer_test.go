package xfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pair runs Serve on one end of two pipes and returns the client's Ctrl.
func pair(t *testing.T) (*Ctrl, chan error) {
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), NewCtrl(br, bw))
		bw.Close()
	}()
	t.Cleanup(func() { aw.Close() })
	return NewCtrl(ar, aw), done
}

func writeTree(t *testing.T, root string) map[string][]byte {
	files := map[string][]byte{
		"empty":            {},
		"small.txt":        []byte("hello"),
		"exact.bin":        make([]byte, ChunkSize),
		"sub/big.bin":      make([]byte, 3*ChunkSize+12345),
		"sub/deep/x.log":   bytes.Repeat([]byte("compressible line\n"), 400000),
		"sub/deep/odd.bin": make([]byte, ChunkSize+1),
	}
	for p, b := range files {
		if len(b) > 0 && p != "sub/deep/x.log" {
			rand.Read(b)
		}
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, b, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	os.Chmod(filepath.Join(root, "small.txt"), 0o755)
	os.Symlink("small.txt", filepath.Join(root, "link"))
	old := time.Unix(1_700_000_000, 0)
	os.Chtimes(filepath.Join(root, "sub/big.bin"), old, old)
	return files
}

func checkTree(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for p, want := range files {
		got, err := os.ReadFile(filepath.Join(root, p))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s differs (%v, %d bytes)", p, err, len(got))
		}
	}
	if fi, _ := os.Stat(filepath.Join(root, "small.txt")); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(root, "sub/big.bin")); fi.ModTime().Unix() != 1_700_000_000 {
		t.Fatalf("mtime %v", fi.ModTime())
	}
	if l, err := os.Readlink(filepath.Join(root, "link")); err != nil || l != "small.txt" {
		t.Fatalf("link %q %v", l, err)
	}
	m, _ := filepath.Glob(filepath.Join(root, "*", ".*hearth-part*"))
	if len(m) > 0 {
		t.Fatalf("part files left: %v", m)
	}
}

func TestCopyOverTheNetwork(t *testing.T) {
	src := filepath.Join(t.TempDir(), "model")
	files := writeTree(t, src)
	dst := t.TempDir()
	for _, tc := range []struct {
		name     string
		send     bool
		compress string
		streams  int
	}{{"push", true, "off", 4}, {"pull", false, "on", 3}} {
		out := filepath.Join(dst, tc.name)
		os.MkdirAll(out, 0o755)
		c, done := pair(t)
		local, remote := src, out
		if !tc.send {
			local, remote = out, src
		}
		res, err := Client(context.Background(), c, tc.send, local, remote, ClientConfig{Opts: Options{Streams: tc.streams}, Compress: tc.compress, NoShared: true})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if err := <-done; err != nil {
			t.Fatalf("%s server: %v", tc.name, err)
		}
		if res.Streams != tc.streams || res.Via != "direct" || res.Bytes != 5*ChunkSize+12345+5+1+int64(len(files["sub/deep/x.log"])) {
			t.Fatalf("%s: %+v", tc.name, res)
		}
		checkTree(t, filepath.Join(out, "model"), files)
		// Again: everything up to date.
		c, done = pair(t)
		res, err = Client(context.Background(), c, tc.send, local, remote, ClientConfig{Opts: Options{Streams: 2}, NoShared: true})
		if err != nil || <-done != nil || res.Bytes != 0 || res.Plan.Skipped != len(files) {
			t.Fatalf("%s again: %+v %v", tc.name, res, err)
		}
	}
}

// When the client cannot reach the server, the server dials back.
func TestReverseConnections(t *testing.T) {
	if len(LocalAddrs()) == 0 {
		t.Skip("no non-loopback address")
	}
	t.Setenv("HEARTH_XFER_VIA", "reverse")
	src := filepath.Join(t.TempDir(), "model")
	files := writeTree(t, src)
	dst := t.TempDir()
	c, done := pair(t)
	res, err := Client(context.Background(), c, true, src, dst+"/", ClientConfig{Opts: Options{Streams: 3}, NoShared: true})
	if err != nil || <-done != nil {
		t.Fatal(err)
	}
	if res.Via != "reverse" || res.Streams != 3 {
		t.Fatalf("%+v", res)
	}
	checkTree(t, filepath.Join(dst, "model"), files)
}

func TestResumeAndNoClobber(t *testing.T) {
	src := filepath.Join(t.TempDir(), "model")
	files := writeTree(t, src)
	dst := t.TempDir()
	m, _ := Scan(src)
	r, p, err := NewReceiver(dst+"/", m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Half of big.bin arrives, then the copy is cut.
	for i, e := range m.Entries {
		if e.Path == "sub/big.bin" {
			b, _ := os.ReadFile(filepath.Join(src, "sub/big.bin"))
			for c := 0; c < 2; c++ {
				if err := r.WriteChunk(i, int64(c)*ChunkSize, b[c*ChunkSize:(c+1)*ChunkSize]); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	r.Abort()
	full := p.Bytes
	c, done := pair(t)
	res, err := Client(context.Background(), c, true, src, dst+"/", ClientConfig{Opts: Options{Streams: 2}, NoShared: true})
	if err != nil || <-done != nil {
		t.Fatal(err)
	}
	if res.Plan.Resumed != 1 || res.Bytes != full-2*ChunkSize {
		t.Fatalf("resume: %+v (full %d)", res.Plan, full)
	}
	checkTree(t, filepath.Join(dst, "model"), files)

	// no-clobber keeps a changed file.
	os.WriteFile(filepath.Join(dst, "model", "small.txt"), []byte("mine"), 0o644)
	c, done = pair(t)
	res, err = Client(context.Background(), c, true, src, dst+"/", ClientConfig{Opts: Options{Streams: 2, NoClobber: true}, NoShared: true})
	if err != nil || <-done != nil || len(res.Plan.Kept) != len(files) {
		t.Fatalf("no-clobber: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "model", "small.txt")); string(b) != "mine" {
		t.Fatal("no-clobber replaced a file")
	}
}

func TestSharedDiskAndSingleFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "w.bin")
	b := make([]byte, 2*ChunkSize+7)
	rand.Read(b)
	os.WriteFile(src, b, 0o600)
	dst := filepath.Join(t.TempDir(), "renamed.bin")
	c, done := pair(t)
	res, err := Client(context.Background(), c, true, src, dst, ClientConfig{})
	if err != nil || <-done != nil || res.Via != "shared disk" {
		t.Fatalf("%+v %v", res, err)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, b) {
		t.Fatal("content")
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), ".hearth-xfer-probe-*")); len(m) != 0 {
		t.Fatalf("probe left: %v", m)
	}
}

func TestUnsafeManifest(t *testing.T) {
	m := &Manifest{Name: "x", Dir: true, Entries: []Entry{{Path: "", Kind: 'd'}, {Path: "../escape", Kind: 'f', Size: 1}}}
	if _, _, err := NewReceiver(t.TempDir(), m, Options{}); err == nil {
		t.Fatal("accepted ../")
	}
	for _, p := range []string{"/abs", "a/../b", "a//b", "./a"} {
		if safeRel(p) {
			t.Fatalf("%q", p)
		}
	}
}

func TestWrongKeyRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() { Accept(l, NewKey()) }()
	if _, _, err := Dial(l.Addr().String(), NewKey(), time.Second); err == nil {
		t.Fatal("a connection with another key was accepted")
	}
}
