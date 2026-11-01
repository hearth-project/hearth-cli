package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/api"
	mach "github.com/hearth-project/hearth-cli/internal/machine"
)

func dev(id, name, gpu, ws string, seen int64) api.Device {
	return api.Device{ID: id, Name: name, Status: "active", LastSeen: seen,
		Machine: map[string]any{"workspace": ws, "facts": map[string]any{"os": "linux", "arch": "x86_64", "gpu": gpu}}}
}

func TestSuggestWorkspace(t *testing.T) {
	ds := []api.Device{
		dev("a", "old-bzz2", "cf-ng-bzz2-o", "/ws/old", 10),
		dev("b", "new-bzz2", "cf-ng-bzz2-o", "/ws/new", 20),
		dev("c", "gb-box", "cf-ng-gbzz2-o-l", "/ws/gb", 30),
		dev("self", "me", "cf-ng-bzz2-o", "/ws/self", 40),
	}
	if s := SuggestWorkspace(ds, "self", mach.Facts{OS: "linux", Arch: "x86_64", GPU: "cf-ng-bzz2-o"}, nil); s.Path != "/ws/new" {
		t.Fatalf("same GPU: %+v", s)
	}
	if s := SuggestWorkspace(ds, "self", mach.Facts{OS: "linux", Arch: "aarch64", GPU: "cf-ng-gbzz2-x"}, nil); s.Path != "/ws/gb" {
		t.Fatalf("same family: %+v", s)
	}
	if s := SuggestWorkspace(ds, "self", mach.Facts{OS: "darwin", Arch: "aarch64", GPU: "apple"}, nil); s.Path != "" {
		t.Fatalf("nothing alike should suggest the home: %+v", s)
	}
	// A workspace on a disk this machine does not mount is skipped and named.
	notNew := func(p string) bool { return p != "/ws/new" && p != "/ws/gb" }
	if s := SuggestWorkspace(ds, "self", mach.Facts{OS: "linux", Arch: "x86_64", GPU: "cf-ng-bzz2-o"}, notNew); s.Path != "/ws/old" || s.Missing != "/ws/new" || s.MissingMachine != "new-bzz2" {
		t.Fatalf("missing disk: %+v", s)
	}
	if s := SuggestWorkspace(ds, "self", mach.Facts{OS: "linux", Arch: "aarch64", GPU: "cf-ng-gbzz2-x"}, notNew); s.Path != "" || s.Missing != "/ws/gb" {
		t.Fatalf("only alike machine on a missing disk: %+v", s)
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "mnt"), 0o755)
	if !WorkspaceUsable(filepath.Join(root, "mnt")) || !WorkspaceUsable(filepath.Join(root, "mnt", "me")) || WorkspaceUsable(filepath.Join(root, "absent", "me")) {
		t.Fatal("WorkspaceUsable")
	}
}

func TestMoveHome(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "home", ".hearth")
	os.MkdirAll(filepath.Join(from, "library"), 0o700)
	os.WriteFile(filepath.Join(from, "config.json"), []byte("{}"), 0o600)
	nh, err := MoveHome(from, filepath.Join(root, "work"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(nh, "config.json")); err != nil || WorkspaceOf(nh) != filepath.Join(root, "work") {
		t.Fatalf("not moved: %v %q", err, WorkspaceOf(nh))
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatal("old home left behind")
	}
	if err := copyTreeX(nh, filepath.Join(root, "copy")); err != nil {
		t.Fatal(err)
	}
}
