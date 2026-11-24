package library

import (
	"os"
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

// Hearth restores ordinary skill files as 0600. A source file, test or
// script must not turn into a credential merely because of that mode; a
// credential name or a binary still does.
func TestOwnerOnlySkillFiles(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "skills/a/run.py"), "print('ok')\n")
	write(t, filepath.Join(d, "skills/a/tests/test_run.py"), "def test_ok():\n    assert True\n")
	write(t, filepath.Join(d, "skills/a/doc.md"), "# Notes\n")
	write(t, filepath.Join(d, "skills/a/bin/tool"), "#!/bin/sh\necho ok\n")
	write(t, filepath.Join(d, "skills/a/credentials"), "token=abc\n")
	write(t, filepath.Join(d, "skills/a/blob.bin"), "\x00\x01\x02")
	for _, rel := range []string{"run.py", "tests/test_run.py", "doc.md", "bin/tool", "credentials", "blob.bin"} {
		if err := os.Chmod(filepath.Join(d, "skills/a", rel), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := ScanSecrets(d)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range fs {
		got[f.File] = f.Rule
	}
	if len(fs) != 2 || got["skills/a/credentials"] != "credential file" || got["skills/a/blob.bin"] != "credential file" {
		t.Fatalf("findings: %v", fs)
	}
}
