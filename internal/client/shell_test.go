package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellUnits(t *testing.T) {
	text := "# my aliases\nalias k=kubectl\n\nf() {\n  if true; then\n\n    echo 'a # b'\n  fi\n}\nexport A=1 \\\n  B=2\nX='multi\n\nline'\n"
	u := shellUnits(text)
	want := []string{"# my aliases\nalias k=kubectl", "f() {\n  if true; then\n\n    echo 'a # b'\n  fi\n}", "export A=1 \\\n  B=2", "X='multi\n\nline'"}
	if len(u) != len(want) {
		t.Fatalf("%d units: %q", len(u), u)
	}
	for i := range want {
		if u[i] != want[i] {
			t.Fatalf("unit %d: %q want %q", i, u[i], want[i])
		}
	}
}

func TestOwnShellAgainstSkel(t *testing.T) {
	skel, err := os.ReadFile("/etc/skel/.bashrc")
	if err != nil {
		t.Skip("no /etc/skel/.bashrc")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	rc := string(skel) + "\n# >>> conda initialize >>>\n__conda_setup=x\n# <<< conda initialize <<<\n\nalias gs='git status'\nexport PATH=\"$HOME/.local/bin:$PATH\"  # added by the hearth installer\n"
	os.WriteFile(filepath.Join(home, ".bashrc"), []byte(rc), 0o644)
	if got := (ShellFile{Path: "~/.bashrc"}).OwnShell(); got != "alias gs='git status'" {
		t.Fatalf("own part %q", got)
	}
}

// Two machines add to the same rc content; a removal on one is not undone
// by the other; restore leaves out what the rc already has and skips
// sourcing files that are missing.
func TestShellSaveRestore(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	homeA, homeB := t.TempDir(), t.TempDir()
	conf := func(m *machine) {
		cfg, _ := UserConfig(m.Home)
		cfg.Shell = nil
		SaveUserConfig(m.Home, cfg)
	}
	conf(a)
	rcA := "alias gs='git status'\n\nexport EDITOR=vim\n\nsource /nonexistent/env.sh\n"
	os.WriteFile(filepath.Join(homeA, ".bash_aliases"), []byte(rcA), 0o644)
	t.Setenv("HOME", homeA)
	saved, err := a.SaveShell()
	if err != nil || len(saved) != 1 || saved[0].Paragraphs != 3 {
		t.Fatalf("save A %+v %v", saved, err)
	}
	if saved, _ = a.SaveShell(); len(saved) != 0 {
		t.Fatal("unchanged file uploaded again")
	}

	t.Setenv("HOME", homeB)
	os.WriteFile(filepath.Join(homeB, ".bash_aliases"), []byte("export EDITOR=vim\n"), 0o644)
	rs, err := b.RestoreShell()
	if err != nil || len(rs) != 1 || rs[0].Paragraphs != 2 || len(rs[0].Missing) != 1 {
		t.Fatalf("restore B %+v %v", rs, err)
	}
	f := ShellFiles(b.Home)[2]
	snip, _ := os.ReadFile(f.snippetPath(b.Home))
	if !strings.Contains(string(snip), "alias gs=") || strings.Contains(string(snip), "EDITOR") || !strings.Contains(string(snip), ": # hearth: not on this machine") {
		t.Fatalf("snippet:\n%s", snip)
	}
	rc, _ := os.ReadFile(filepath.Join(homeB, ".bashrc"))
	if !strings.Contains(string(rc), f.hookPath(b.Home)) {
		t.Fatalf("rc not hooked:\n%s", rc)
	}
	if _, err := b.RestoreShell(); err != nil {
		t.Fatal(err)
	}
	if rc2, _ := os.ReadFile(filepath.Join(homeB, ".bashrc")); string(rc2) != string(rc) {
		t.Fatal("second restore changed the rc")
	}
	// B adds an alias of its own; nothing restored is lost.
	os.WriteFile(filepath.Join(homeB, ".bash_aliases"), []byte("export EDITOR=vim\n\nalias k=kubectl\n"), 0o644)
	if _, err := b.SaveShell(); err != nil {
		t.Fatal(err)
	}
	s, _ := b.Secret(f.Secret)
	for _, want := range []string{"alias gs=", "EDITOR", "alias k=", "/nonexistent"} {
		if !strings.Contains(string(s.Value), want) {
			t.Fatalf("merged lacks %s:\n%s", want, s.Value)
		}
	}
	// A drops EDITOR: gone from the stored copy, B's alias stays.
	t.Setenv("HOME", homeA)
	os.WriteFile(filepath.Join(homeA, ".bash_aliases"), []byte("alias gs='git status'\n\nsource /nonexistent/env.sh\n"), 0o644)
	if _, err := a.SaveShell(); err != nil {
		t.Fatal(err)
	}
	s, _ = a.Secret(f.Secret)
	if strings.Contains(string(s.Value), "EDITOR") || !strings.Contains(string(s.Value), "alias k=") {
		t.Fatalf("after removal:\n%s", s.Value)
	}
}

func TestShellHookSyntax(t *testing.T) {
	for _, shared := range []bool{false, true} {
		p := filepath.Join(t.TempDir(), "h.sh")
		os.WriteFile(p, shellHook("/tmp/x it's.sh", "/ws/my dir", shared, "shell-bashrc"), 0o600)
		if out, err := exec.Command("bash", "-n", p).CombinedOutput(); err != nil {
			t.Fatalf("shared=%v: %v %s", shared, err, out)
		}
	}
}

func TestNeutralizeGuardsMissingTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	in := "conda activate py312\n  eval \"$(starship init bash)\"\nconda() {\nexport X=1\nls -la"
	out, _ := neutralize(in)
	lines := strings.Split(out, "\n")
	if !strings.HasPrefix(lines[0], "command -v conda >/dev/null 2>&1 && conda activate py312") {
		t.Fatalf("conda not guarded: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  command -v starship >/dev/null 2>&1 && eval") {
		t.Fatalf("eval not guarded: %q", lines[1])
	}
	if lines[2] != "conda() {" || lines[3] != "export X=1" || lines[4] != "ls -la" {
		t.Fatalf("other lines changed: %q", lines[2:])
	}
}
