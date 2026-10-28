package library

import (
	"path/filepath"
	"testing"
)

func TestScanSecrets(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "skills/a/config.yaml"), "access_key: \"6+uUabcdefghijklmnop\"\n")
	write(t, filepath.Join(d, "skills/a/run.py"), "token = get_token_for_project(name)\nkey = os.environ['API_KEY']\n")
	write(t, filepath.Join(d, "skills/a/SKILL.md"), "api_key: <your api key>\npassword = ${PASSWORD_FROM_ENV}\n-----BEGIN OPENSSH PRIVATE KEY-----\n")
	write(t, filepath.Join(d, "skills/a/doc.md"), "token: abcdefghijklmnopqrstuv  # hearth:allow-secret\n")
	write(t, filepath.Join(d, "recipes/r.toml"), "secret = \"id_ed25519_github\"\nstdin_secret = \"codex-mixin-config\"\nenv = { T = \"${secret:GITHUB_PERSONAL_ACCESS_TOKEN}\" }\n")
	fs, err := ScanSecrets(d)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range fs {
		got[f.File] = true
	}
	if len(fs) != 2 || !got["skills/a/config.yaml"] || !got["skills/a/SKILL.md"] {
		t.Fatalf("findings: %v", fs)
	}
}
