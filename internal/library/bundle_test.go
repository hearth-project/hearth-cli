package library

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBundleDeterministicAndRoundTrip(t *testing.T) {
	d := t.TempDir()
	l := Library{Root: filepath.Join(d, "lib")}
	write(t, filepath.Join(l.Root, "skills/a/SKILL.md"), "a\n")
	write(t, filepath.Join(l.Root, "skills/a/run.sh"), "#!/bin/sh\n")
	os.Chmod(filepath.Join(l.Root, "skills/a/run.sh"), 0o755)
	write(t, filepath.Join(l.Root, ".git/config"), "ignored\n")
	_, s1, err := l.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	os.Chtimes(filepath.Join(l.Root, "skills/a/SKILL.md"), time.Now(), time.Now().Add(time.Hour))
	data, s2, _ := l.Bundle()
	if s1 != s2 {
		t.Fatal("hash depends on mtime")
	}
	out := filepath.Join(d, "out")
	got, err := Extract(data, out)
	if err != nil || got != s1 {
		t.Fatalf("extract: %v %s %s", err, got, s1)
	}
	if st, _ := os.Stat(filepath.Join(out, "skills/a/run.sh")); st.Mode().Perm()&0o111 == 0 {
		t.Fatal("exec bit lost")
	}
	if _, err := os.Stat(filepath.Join(out, ".git")); !os.IsNotExist(err) {
		t.Fatal("dotfiles bundled")
	}
	write(t, filepath.Join(l.Root, "skills/a/SKILL.md"), "changed\n")
	if _, s3, _ := l.Bundle(); s3 == s1 {
		t.Fatal("hash ignores content")
	}
}

func TestExtractRejectsUnsafeEntries(t *testing.T) {
	mk := func(h *tar.Header, body string) []byte {
		var raw bytes.Buffer
		tw := tar.NewWriter(&raw)
		h.Size = int64(len(body))
		tw.WriteHeader(h)
		tw.Write([]byte(body))
		tw.Close()
		var gz bytes.Buffer
		zw := gzip.NewWriter(&gz)
		zw.Write(raw.Bytes())
		zw.Close()
		return gz.Bytes()
	}
	cases := map[string]*tar.Header{
		"dotdot":  {Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644},
		"abs":     {Name: "/etc/evil", Typeflag: tar.TypeReg, Mode: 0o644},
		"unclean": {Name: "skills/../../evil", Typeflag: tar.TypeReg, Mode: 0o644},
		"symlink": {Name: "skills/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
	}
	for name, h := range cases {
		if _, err := Extract(mk(h, "x"), filepath.Join(t.TempDir(), "out")); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
