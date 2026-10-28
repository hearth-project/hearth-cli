package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/library"
)

func testBundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// Items come out of the library bundle in the package layout; the secret
// scan sees what would be shared; paths cannot escape.
func TestExtractItem(t *testing.T) {
	b := testBundle(t, map[string]string{
		"skills/leaky/SKILL.md":   "---\nname: leaky\n---\napi_key = abcdefghijklmnopqrstuvwxyz123456\n",
		"skills/leaky/run.sh":     "echo\n",
		"skills/leakyother/x.md":  "not this one\n",
		"agents/rev.md":           "---\nname: rev\ndescription: d\n---\nx\n",
		"skills/evil/../../x.txt": "escape\n",
	})
	dir := t.TempDir()
	if err := extractItem(b, "skill", "leaky", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "run.sh")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.md")); err == nil {
		t.Fatal("took a file of another skill")
	}
	if fs, err := library.ScanSecrets(dir); err != nil || len(fs) == 0 {
		t.Fatalf("secret not found: %v %v", fs, err)
	}
	ad := t.TempDir()
	if err := extractItem(b, "agent", "rev", ad); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ad, "AGENT.md")); err != nil {
		t.Fatal(err)
	}
	if err := extractItem(b, "skill", "missing", t.TempDir()); err == nil {
		t.Fatal("missing skill extracted")
	}
}
