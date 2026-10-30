package client

import (
	"github.com/hearth-project/hearth-cli/internal/library"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Setup's import: the machine's own skills and agents go into the library
// once; credentials stay out; a deleted item is not brought back.
func TestAutoImport(t *testing.T) {
	en := newEnv(t)
	m := en.machine("a")
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	t.Setenv("HOME", root)
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("HEARTH_COMATE_HOME", filepath.Join(root, "comate"))
	write := func(rel, content string) {
		p := filepath.Join(codex, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/demo/SKILL.md", "---\nname: demo\ndescription: demo\n---\nsteps\n")
	write("skills/leaky/SKILL.md", "---\nname: leaky\ndescription: x\n---\napi_key = abcdefghijklmnop1234\n")
	write("agents/rev.toml", "name = \"rev\"\ndescription = \"reviews\"\ndeveloper_instructions = \"Review.\"\n")
	write("AGENTS.md", "machine notes\n")

	r, err := m.AutoImport()
	if err != nil {
		t.Fatal(err)
	}
	// A skill with a credential is imported: the credential file travels
	// encrypted, not in the library bundle (libsecrets.go).
	if !slices.Equal(r.Imported, []string{"agent:rev", "skill:demo", "skill:leaky"}) || len(r.Blocked) != 0 {
		t.Fatalf("imported %v blocked %v", r.Imported, r.Blocked)
	}
	lib := LibraryDir(m.Home)
	if fs := libSecretFiles(library.Library{Root: lib}); fs["skills/leaky/SKILL.md"] == "" {
		t.Fatalf("credential of the imported skill not kept apart: %v", fs)
	}
	if _, err := os.Stat(filepath.Join(lib, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("instructions imported")
	}
	// Deleted from the library: stays deleted.
	os.RemoveAll(filepath.Join(lib, "skills/demo"))
	if r, err = m.AutoImport(); err != nil || len(r.Imported) != 0 {
		t.Fatalf("second run %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(lib, "skills/demo")); !os.IsNotExist(err) {
		t.Fatal("deleted skill brought back")
	}
	// A new skill later is still picked up.
	write("skills/fresh/SKILL.md", "---\nname: fresh\ndescription: new\n---\nx\n")
	if r, _ = m.AutoImport(); !slices.Equal(r.Imported, []string{"skill:fresh"}) {
		t.Fatalf("new skill %v", r.Imported)
	}
}
