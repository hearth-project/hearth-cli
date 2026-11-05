package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/datatool"
	"github.com/hearth-project/hearth-cli/internal/library"
)

func testTransferEnv() *transferEnv {
	return &transferEnv{
		user: "alice",
		cfg: library.TransferConfig{Paths: map[string][]library.TransferPathRule{
			"gpfs@cn": {{From: "/mnt/personal", To: "/data/{user}"}},
		}},
	}
}

func TestParseStoragePath(t *testing.T) {
	env := testTransferEnv()
	storages := []string{"gpfs@cn", "weka@johor", "afs@cn"}
	cases := []struct {
		arg    string
		want   datatool.StoragePath
		mapped bool
	}{
		{"gpfs@cn:/mnt/personal/m", datatool.StoragePath{Storage: "gpfs@cn", Path: "/data/alice/m"}, true},
		{"gpfs@cn:/data/other", datatool.StoragePath{Storage: "gpfs@cn", Path: "/data/other"}, false},
		{"weka@johor:/mnt/personal/m", datatool.StoragePath{Storage: "weka@johor", Path: "/mnt/personal/m"}, false}, // rules are per storage
		{"afs@cn:afs://c/u/d", datatool.StoragePath{Storage: "afs@cn", Path: "afs://c/u/d"}, false},
	}
	for _, c := range cases {
		got, mapped, err := env.parseStoragePath(storages, c.arg)
		if err != nil || got != c.want || mapped != c.mapped {
			t.Errorf("%s: got %+v mapped=%v err=%v; want %+v mapped=%v", c.arg, got, mapped, err, c.want, c.mapped)
		}
	}
	for _, bad := range []string{"gpfs@cn:", "other:/x", "/x", "gpfs@cn:relative/dir", "gpfs@cn:../x", "gpfs@cn:/a/../b"} {
		if _, _, err := env.parseStoragePath(storages, bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestParseStoragePathRelativeWithRule(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir()) // the working directory is reported without symlinks
	if err != nil {
		t.Fatal(err)
	}
	env := testTransferEnv()
	env.cfg.Paths["gpfs@cn"] = []library.TransferPathRule{{From: dir, To: "/data/{user}/run"}}
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	// A relative path becomes absolute here and is then translated by the
	// rule that covers it.
	got, mapped, err := env.parseStoragePath([]string{"gpfs@cn"}, "gpfs@cn:ckpt")
	if err != nil || !mapped || got.Path != "/data/alice/run/ckpt" {
		t.Fatalf("got %+v mapped=%v err=%v", got, mapped, err)
	}
}

func TestTransferToken(t *testing.T) {
	t.Setenv("HEARTH_TEST_DT_TOKEN", " abc ")
	if v, err := transferToken(library.TransferConfig{TokenEnv: "HEARTH_TEST_DT_TOKEN"}); err != nil || v != "abc" {
		t.Fatalf("env token: %q %v", v, err)
	}
	if v, err := transferToken(library.TransferConfig{}); err != nil || v != "" {
		t.Fatalf("no token configured: %q %v", v, err)
	}
	t.Setenv("HEARTH_TEST_DT_TOKEN", "")
	if _, err := transferToken(library.TransferConfig{TokenEnv: "HEARTH_TEST_DT_TOKEN"}); err == nil || !strings.Contains(err.Error(), "HEARTH_TEST_DT_TOKEN") {
		t.Fatalf("an empty variable must be reported: %v", err)
	}
}

func TestDefaultTransferName(t *testing.T) {
	day := time.Now().Format("20060102")
	if got := defaultTransferName("/data/alice/My Model v1/", 128); got != "hearth-My-Model-v1-"+day {
		t.Errorf("name = %q", got)
	}
	if got := defaultTransferName("/data/模型", 128); got != "hearth-"+day {
		t.Errorf("a non-ASCII directory name must not leave odd characters: %q", got)
	}
	if got := defaultTransferName("/data/"+strings.Repeat("x", 300), 20); len([]rune(got)) != 20 {
		t.Errorf("name not cut to the limit: %q", got)
	}
}

func TestProgressText(t *testing.T) {
	p := datatool.Progress{Bytes: 5 << 30, TotalBytes: 20 << 30, SpeedBps: 300 << 20, ETASeconds: 125}
	if got := progressLong(p); got != "5.0 GiB / 20 GiB (25%) · 300 MiB/s · ETA 2m5s" {
		t.Errorf("progress = %q", got)
	}
	if progressLong(datatool.Progress{}) != "" || progressShort(datatool.Task{}) != "-" {
		t.Error("an empty progress must print nothing")
	}
	if got := progressShort(datatool.Task{Progress: datatool.Progress{Bytes: 3, TotalBytes: 2}}); got != "100%" {
		t.Errorf("over 100%%: %q", got)
	}
}
