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

// Hearth restores ordinary skill files as 0600. Source files and SVG assets
// should keep their type unless a name or content rule recognizes a credential.
func TestOwnerOnlySkillFiles(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "skills/a/run.py"), "print('ok')\n")
	write(t, filepath.Join(d, "skills/a/tests/test_run.py"), "def test_ok():\n    assert True\n")
	write(t, filepath.Join(d, "skills/a/doc.md"), "# Notes\n")
	write(t, filepath.Join(d, "skills/a/bin/tool"), "#!/bin/sh\necho ok\n")
	write(t, filepath.Join(d, "skills/a/icons/check.svg"), `<svg xmlns="http://www.w3.org/2000/svg"><path d="M1 5l3 3 5-6"/></svg>`)
	write(t, filepath.Join(d, "skills/a/icons/private.svg"), `<svg xmlns="http://www.w3.org/2000/svg"><metadata token="9Btwc3eYzqV7hGs1L8uP"/></svg>`)
	write(t, filepath.Join(d, "skills/a/credentials"), "token=abc\n")
	write(t, filepath.Join(d, "skills/a/blob.bin"), "\x00\x01\x02")
	for _, rel := range []string{"run.py", "tests/test_run.py", "doc.md", "bin/tool", "icons/check.svg", "icons/private.svg", "credentials", "blob.bin"} {
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
	if len(fs) != 3 || got["skills/a/credentials"] != "credential file" || got["skills/a/blob.bin"] != "credential file" ||
		got["skills/a/icons/private.svg"] != "credential assignment" {
		t.Fatalf("findings: %v", fs)
	}
}

func TestOwnerOnlyMediaUsesFileSignature(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	want := make(map[string]bool)
	for _, tc := range []struct {
		name, content string
		private       bool
	}{
		{"image.PNG", "\x89PNG\r\n\x1a\n\x00", false},
		{"image.webp", "RIFF\x04\x00\x00\x00WEBPVP8 ", false},
		{"sound.wav", "RIFF\x04\x00\x00\x00WAVE", false},
		{"renamed.wav", "\x89PNG\r\n\x1a\n\x00", true},
		{"unknown.png", "\x01\x02\x03\x04", true},
		{"id_ed25519.png", "\x89PNG\r\n\x1a\n\x00", true},
	} {
		path := filepath.Join(dir, "example", tc.name)
		write(t, path, tc.content)
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if tc.private {
			want[filepath.Join("example", tc.name)] = true
		}
	}
	findings, err := ScanSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if !want[finding.File] {
			t.Errorf("unexpected finding: %v", finding)
		}
		delete(want, finding.File)
	}
	if len(want) > 0 {
		t.Errorf("files that should remain private: %v", want)
	}
}
