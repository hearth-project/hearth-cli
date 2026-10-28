package client

import (
	"testing"
	"time"
)

// The daemon and a CLI command share state.json: neither may drop the
// other's entries, and deletions must stick.
func TestStateSharedByTwoProcesses(t *testing.T) {
	home := t.TempDir()
	daemon, _ := loadState(home)
	cli, _ := loadState(home)
	if err := daemon.put("old", &fileState{Logical: "codex/old"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // distinct mtimes
	if err := cli.put("restored", &fileState{Logical: "codex/restored", Restored: true}); err != nil {
		t.Fatal(err)
	}
	cli.setRemote("restored", "codex/local-name", &remoteInfo{Meta: SessionMeta{Agent: "codex", RelPath: "x"}})
	time.Sleep(10 * time.Millisecond)
	// The daemon writes without having seen the CLI's restore.
	if err := daemon.put("old", nil); err != nil {
		t.Fatal(err)
	}
	disk, _ := loadState(home)
	if disk.get("restored") == nil || disk.remote("restored") == nil || disk.alias("codex/local-name") != "restored" {
		t.Fatalf("daemon dropped the CLI's restore: %+v", disk.data)
	}
	if disk.get("old") != nil {
		t.Fatal("deleted entry merged back")
	}
	if err := daemon.refresh(); err != nil || daemon.get("restored") == nil {
		t.Fatalf("refresh: %v", err)
	}
}
