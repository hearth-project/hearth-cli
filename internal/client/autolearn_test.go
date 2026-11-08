package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The daemon records new findings by itself: settings at once, credentials
// too unless auto_credentials is off; changed copies and passwords in proxy URLs
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
	// auto_credentials explicitly off: the credential waits for the user.
	if err := SetAutoCredentials(a.Home, false); err != nil {
		t.Fatal(err)
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
	// Default (not set): credentials are recorded without asking.
	cfg0, _ := UserConfig(a.Home)
	cfg0.Learn.AutoCredentials = nil
	if err := SaveUserConfig(a.Home, cfg0); err != nil {
		t.Fatal(err)
	}
	if on, decided := AutoCredentials(a.Home); !on || decided {
		t.Fatalf("default auto_credentials %v %v", on, decided)
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

// A skill declares the credential files it keeps outside its directory;
// learning records them as the skill's secrets and another machine gets
// them back by the skill's name.
func TestSkillCredentials(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	home, _ := learnEnv(t)
	libWrite(t, a, "skills/kudoc/SKILL.md", "---\nname: kudoc\ndescription: d\nmetadata:\n  hearth:\n    credentials:\n      - ~/.config/kudoc/auth.yaml\n      - ~/.config/uuap/token_${HEARTH_USER}\n      - ../escape\n---\nbody\n")
	os.MkdirAll(filepath.Join(home, ".config/kudoc"), 0o700)
	os.MkdirAll(filepath.Join(home, ".config/uuap"), 0o700)
	os.WriteFile(filepath.Join(home, ".config/kudoc/auth.yaml"), []byte("access_key: k\n"), 0o600)
	os.WriteFile(filepath.Join(home, ".config/uuap/token_dev"), []byte("tok"), 0o600) // dev@example.com
	os.WriteFile(filepath.Join(home, ".config/uuap/token_other"), []byte("no"), 0o600)
	if _, err := a.AutoLearn(); err != nil {
		t.Fatal(err)
	}
	all, _ := a.Secrets()
	got := map[string]string{}
	for _, s := range all {
		if skillOfSecret(s.Name) == "kudoc" {
			got[s.RestoreTo] = string(s.Value)
		}
	}
	if got["~/.config/kudoc/auth.yaml"] != "access_key: k\n" || got["~/.config/uuap/token_dev"] != "tok" || len(got) != 2 {
		t.Fatalf("skill secrets %v", got)
	}
	os.RemoveAll(filepath.Join(home, ".config/kudoc"))
	os.Remove(filepath.Join(home, ".config/uuap/token_dev"))
	us, err := b.UseCredentials([]string{"kudoc"}, time.Hour)
	if err != nil || len(us) != 2 {
		t.Fatalf("use: %+v %v", us, err)
	}
	if v, _ := os.ReadFile(filepath.Join(home, ".config/kudoc/auth.yaml")); string(v) != "access_key: k\n" {
		t.Fatalf("restored %q", v)
	}
	// Declared but never recorded: the error says what is missing.
	libWrite(t, b, "skills/other/SKILL.md", "---\nname: other\ndescription: d\nmetadata:\n  hearth:\n    credentials: [~/.config/other.yaml]\n---\nx\n")
	if _, err := b.UseCredentials([]string{"other"}, time.Hour); err == nil || !strings.Contains(err.Error(), "~/.config/other.yaml") {
		t.Fatalf("missing: %v", err)
	}
}
