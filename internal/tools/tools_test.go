package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBin puts executable scripts on PATH.
func fakeBin(t *testing.T, scripts map[string]string) string {
	dir := t.TempDir()
	for name, body := range scripts {
		os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return dir
}

func TestSyncBuiltinAndCustom(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "npm.log")
	bin := fakeBin(t, map[string]string{
		"codex": "echo 'codex-cli 0.150.0'\n",
		// npm "installs" codex by rewriting the fake binary.
		"npm": "echo \"$@\" >> " + log + "\nprintf '#!/bin/sh\\necho codex-cli 0.163.0\\n' > \"$(dirname \"$0\")/codex\"\n",
	})
	m := &Manifest{}
	if err := m.Set(Tool{Name: "codex", Version: "0.163.0"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Set(Tool{Name: "codex", Version: "1.0; rm -rf /"}); err == nil {
		t.Fatal("unsafe version accepted")
	}
	if err := m.Set(Tool{Name: "dumate"}); err == nil {
		t.Fatal("custom tool without commands accepted")
	}
	marker := filepath.Join(dir, "dumate-installed")
	if err := m.Set(Tool{Name: "dumate", Check: "cat " + marker, Install: "echo 'dumate 2.0.0' > " + marker}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "tools.json")
	m.Save(p)
	m, _ = Load(p)

	ap := Approvals{}
	st := Sync(m, ap, false, false)
	byName := map[string]Status{}
	for _, s := range st {
		byName[s.Tool.Name] = s
	}
	if s := byName["codex"]; s.Action != "installed" || s.Installed != "0.163.0" {
		t.Fatalf("codex: %+v", s)
	}
	if b, _ := os.ReadFile(log); strings.TrimSpace(string(b)) != "install -g @openai/codex@0.163.0" {
		t.Fatalf("npm args: %q", b)
	}
	if s := byName["dumate"]; s.Action != "needs-approval" {
		t.Fatalf("custom ran without approval: %+v", s)
	}
	st = Sync(m, ap, true, false)
	for _, s := range st {
		byName[s.Tool.Name] = s
	}
	if byName["codex"].Action != "ok" || byName["dumate"].Action != "installed" || byName["dumate"].Installed != "2.0.0" {
		t.Fatalf("second sync: %+v", st)
	}
	// Changing the synced command invalidates the approval.
	os.Remove(marker)
	m.Set(Tool{Name: "dumate", Check: "cat " + marker, Install: "echo pwned > " + marker})
	for _, s := range Sync(m, ap, false, false) {
		if s.Tool.Name == "dumate" && s.Action != "needs-approval" {
			t.Fatalf("modified command ran with stale approval: %+v", s)
		}
	}
	_ = bin
}

// A version the local npm registry does not have keeps the installed one.
func TestSyncPinnedVersionMissingFromRegistry(t *testing.T) {
	fakeBin(t, map[string]string{
		"codex": "echo 'codex-cli 0.162.1'\n",
		"npm":   "echo 'npm error code ETARGET' >&2\nexit 1\n",
	})
	m := &Manifest{}
	m.Set(Tool{Name: "codex", Version: "0.163.0"})
	st := Sync(m, Approvals{}, false, false)
	if len(st) != 1 || st[0].Action != "kept" || st[0].Installed != "0.162.1" {
		t.Fatalf("%+v", st)
	}
}

// Setup never replaces a working CLI and skips agents not restored here.
func TestSyncMissingOnly(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "npm.log")
	fakeBin(t, map[string]string{
		"codex": "echo 'codex-cli 0.162.1'\n",
		"npm":   "echo \"$@\" >> " + log + "\n",
	})
	m := &Manifest{}
	m.Set(Tool{Name: "codex", Version: "0.163.0"})
	m.Set(Tool{Name: "claude", Version: "2.0.0"})
	st := SyncWith(m, Approvals{}, SyncOptions{MissingOnly: true, Skip: map[string]bool{"claude": true}})
	if len(st) != 1 || st[0].Action != "kept" || st[0].Installed != "0.162.1" {
		t.Fatalf("%+v", st)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("npm ran")
	}
}
