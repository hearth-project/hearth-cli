package client

import (
	"os"
	"path/filepath"
	"testing"
)

// The daemon records new findings by itself: settings at once, credentials
// once auto_credentials is on; changed copies and passwords in proxy URLs
// are left to the user.
func TestAutoLearn(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	home, _ := learnEnv(t)
	t.Setenv("npm_config_registry", "https://registry.example.com")
	t.Setenv("HTTPS_PROXY", "http://me:secret@proxy:3128")
	os.MkdirAll(filepath.Join(home, ".config/gh"), 0o700)
	gh := filepath.Join(home, ".config/gh/hosts.yml")
	os.WriteFile(gh, []byte("github.com:\n  oauth_token: gho_x\n"), 0o600)

	ids := func(rs []AutoLearned) map[string]bool {
		m := map[string]bool{}
		for _, r := range rs {
			if r.Failed {
				t.Fatalf("%s failed: %s", r.ID, r.Detail)
			}
			m[r.ID] = true
		}
		return m
	}
	got, err := a.AutoLearn()
	if err != nil {
		t.Fatal(err)
	}
	if m := ids(got); !m["env:npm_config_registry"] || m["gh-hosts"] || m["env:HTTPS_PROXY"] {
		t.Fatalf("first run %v", m)
	}
	pend := findingIDs(a.LearnPending())
	if _, ok := pend["gh-hosts"]; !ok {
		t.Fatalf("credential not pending: %v", pend)
	}
	if _, ok := pend["env:npm_config_registry"]; ok {
		t.Fatal("recorded item still pending")
	}
	if err := SetAutoCredentials(a.Home, true); err != nil {
		t.Fatal(err)
	}
	got, _ = a.AutoLearn()
	if m := ids(got); !m["gh-hosts"] || len(m) != 1 {
		t.Fatalf("with auto_credentials %v", m)
	}
	if got, _ = a.AutoLearn(); len(got) != 0 {
		t.Fatalf("third run recorded again: %+v", got)
	}
	if s, err := a.Secret("file-config-gh-hosts.yml"); err != nil || string(s.Value) == "" {
		names := []string{}
		all, _ := a.Secrets()
		for _, x := range all {
			names = append(names, x.Name)
		}
		t.Fatalf("secret not stored: %v %v", err, names)
	}
	// A changed login is not overwritten by itself.
	os.WriteFile(gh, []byte("github.com:\n  oauth_token: gho_y\n"), 0o600)
	if got, _ = a.AutoLearn(); len(got) != 0 {
		t.Fatalf("changed copy recorded automatically: %+v", got)
	}
	if _, ok := findingIDs(a.LearnPending())["gh-hosts"]; !ok {
		t.Fatal("changed copy not pending")
	}
	// Off: nothing recorded.
	off := false
	cfg, _ := UserConfig(a.Home)
	cfg.Learn.Auto = &off
	SaveUserConfig(a.Home, cfg)
	t.Setenv("PIP_INDEX_URL", "https://pypi.example.com/simple")
	if got, _ = a.AutoLearn(); len(got) != 0 {
		t.Fatalf("auto = false still recorded %+v", got)
	}
}
