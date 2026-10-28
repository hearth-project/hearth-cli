package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsDirFromConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	ext := filepath.Join(os.Getenv("HOME"), "my-skills")
	lib := Library{Root: root}
	if err := lib.SaveConfig(Config{Skills: &SkillsConfig{Dir: "${HOME}/my-skills"}}); err != nil {
		t.Fatal(err)
	}
	if got := lib.SkillsDir(); got != filepath.Join(os.Getenv("HOME"), "my-skills") {
		t.Fatalf("skills dir %q", got)
	}
	// The bundle carries them under skills/ and the setting itself.
	os.MkdirAll(filepath.Join(ext, "foo"), 0o755)
	os.WriteFile(filepath.Join(ext, "foo", "SKILL.md"), []byte("---\nname: foo\ndescription: d\n---\nx\n"), 0o644)
	data, _, err := lib.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if _, err := Extract(data, filepath.Join(out, "lib")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(out, "lib", "skills", "foo", "SKILL.md")); err != nil || !strings.Contains(string(b), "name: foo") {
		t.Fatalf("skill not bundled: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(out, "lib", "config.toml")); err != nil || !strings.Contains(string(b), "my-skills") {
		t.Fatalf("config not bundled: %v", err)
	}
}

func TestSkillsDirRejectsBadConfig(t *testing.T) {
	lib := Library{Root: t.TempDir()}
	os.WriteFile(lib.ConfigPath(), []byte("[skills]\ndir = \"relative/dir\"\n"), 0o644)
	if _, err := lib.LoadConfig(); err == nil {
		t.Fatal("relative skills.dir accepted")
	}
	if got := lib.SkillsDir(); got != filepath.Join(lib.Root, "skills") {
		t.Fatalf("bad config must fall back, got %q", got)
	}
}

func TestRenderFromExternalSkillsDir(t *testing.T) {
	d := t.TempDir()
	lib := Library{Root: filepath.Join(d, "lib")}
	ext := filepath.Join(d, "ext")
	os.MkdirAll(filepath.Join(ext, "bench"), 0o755)
	os.WriteFile(filepath.Join(ext, "bench", "SKILL.md"), []byte("---\nname: bench\ndescription: d\n---\nx\n"), 0o644)
	if err := lib.SaveConfig(Config{Skills: &SkillsConfig{Dir: ext}}); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(d, "codex")
	acts, err := Render(lib, []Target{CodexTarget(codex)}, Options{Manifest: filepath.Join(d, "render.json")})
	if err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(codex, "skills", "bench"))
	if err != nil || link != filepath.Join(ext, "bench") {
		t.Fatalf("link %q %v (acts %+v)", link, err, acts)
	}
}
