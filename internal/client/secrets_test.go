package client

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretsEndToEndEncrypted(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	ghPath := filepath.Join(t.TempDir(), "gh", "hosts.yml")
	if err := a.SetSecret(Secret{Name: "GITHUB_TOKEN", Value: []byte("ghp_TOPSECRETVALUE")}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetSecret(Secret{Name: "gh-hosts", Value: []byte("oauth_token: FILESECRET\n"), RestoreTo: ghPath}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Secret{{Name: "../x", Value: []byte("v")}, {Name: "x", Value: nil}, {Name: "x", Value: []byte("v"), RestoreTo: "relative/path"}} {
		if err := a.SetSecret(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}

	// Another device of the same account sees and restores them.
	all, err := b.Secrets()
	if err != nil || len(all) != 2 || string(all[0].Value) != "ghp_TOPSECRETVALUE" {
		t.Fatalf("list: %+v %v", all, err)
	}
	env, _ := b.SecretEnv()
	if len(env) != 1 || env[0] != "GITHUB_TOKEN=ghp_TOPSECRETVALUE" {
		t.Fatalf("env: %v", env)
	}
	rs, err := b.RestoreSecretFiles(false)
	if err != nil || len(rs) != 1 || rs[0].Action != "written" {
		t.Fatalf("restore: %+v %v", rs, err)
	}
	if st, _ := os.Stat(ghPath); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	os.WriteFile(ghPath, []byte("local edit\n"), 0o600)
	if rs, _ := b.RestoreSecretFiles(false); !strings.HasPrefix(rs[0].Action, "skipped") {
		t.Fatalf("existing file overwritten without --force: %+v", rs)
	}

	// Another account cannot read them, and the server stores no plaintext.
	eve := en.user("eve@example.com", "eve")
	if s, _ := eve.Secrets(); len(s) != 0 {
		t.Fatal("another user sees secrets")
	}
	filepath.WalkDir(en.data, func(p string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			c, _ := os.ReadFile(p)
			for _, n := range []string{"TOPSECRETVALUE", "FILESECRET", "GITHUB_TOKEN", "hosts.yml"} {
				if strings.Contains(string(c), n) {
					t.Errorf("%s contains %q", p, n)
				}
			}
		}
		return nil
	})
	if err := a.DeleteSecret("GITHUB_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteSecret("GITHUB_TOKEN"); err == nil {
		t.Fatal("double delete succeeded")
	}
}
