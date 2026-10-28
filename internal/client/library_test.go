package client

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/library"
)

func libWrite(t *testing.T, m *machine, rel, content string) {
	t.Helper()
	p := filepath.Join(LibraryDir(m.Home), rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (m *machine) libTargets(t *testing.T) (codex, claude string) {
	codex, claude = filepath.Join(m.Home, "t-codex"), filepath.Join(m.Home, "t-claude")
	m.LibTargets = []library.Target{library.CodexTarget(codex), library.ClaudeTarget(claude)}
	return
}

const agentMD = "---\nname: helper\ndescription: helps\n---\nBe helpful.\n"

func TestLibrarySyncAcrossMachines(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	aCodex, _ := a.libTargets(t)
	bCodex, bClaude := b.libTargets(t)

	// New machine with nothing anywhere: auto sync is a no-op.
	if r, err := b.LibAuto(); err != nil || r.Op != "empty" {
		t.Fatalf("empty auto: %+v %v", r, err)
	}

	libWrite(t, a, "skills/bench/SKILL.md", "---\nname: bench\ndescription: d\n---\nrun\n")
	libWrite(t, a, "skills/bench/scripts/run.sh", "#!/bin/sh\necho run\n")
	libWrite(t, a, "agents/helper.md", agentMD)
	if r, err := a.LibPush(false); err != nil || r.Op != "pushed" || r.Version != 1 {
		t.Fatalf("push: %+v %v", r, err)
	}
	if r, _ := a.LibPush(false); r.Op != "up-to-date" {
		t.Fatalf("second push: %+v", r)
	}

	// b pulls and renders for both agents.
	if r, err := b.LibAuto(); err != nil || r.Op != "pulled" {
		t.Fatalf("b auto: %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(bCodex, "skills/bench/scripts/run.sh")); err != nil {
		t.Fatalf("skill not rendered on b: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bClaude, "agents/helper.md")); err != nil {
		t.Fatalf("agent not rendered for claude on b: %v", err)
	}
	if r, err := b.LibAuto(); err != nil || r.Op != "up-to-date" {
		t.Fatalf("b idle auto: %+v %v", r, err)
	}

	// Edit on b propagates back to a and is rendered there.
	libWrite(t, b, "agents/helper.md", strings.Replace(agentMD, "Be helpful.", "Be very helpful.", 1))
	if r, err := b.LibAuto(); err != nil || r.Op != "pushed" {
		t.Fatalf("b push: %+v %v", r, err)
	}
	if r, err := a.LibAuto(); err != nil || r.Op != "pulled" {
		t.Fatalf("a pull: %+v %v", r, err)
	}
	if b, _ := os.ReadFile(filepath.Join(aCodex, "agents/helper.toml")); !strings.Contains(string(b), "Be very helpful.") {
		t.Fatalf("a not updated:\n%s", b)
	}

	// Concurrent edits: second writer gets a conflict, nothing is lost.
	libWrite(t, a, "agents/helper.md", strings.Replace(agentMD, "Be helpful.", "A says hi.", 1))
	libWrite(t, b, "agents/helper.md", strings.Replace(agentMD, "Be helpful.", "B says hi.", 1))
	if _, err := a.LibAuto(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.LibAuto(); !errors.Is(err, ErrLibraryConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if _, err := b.LibPull(true); err != nil {
		t.Fatal(err)
	}
	baks, _ := filepath.Glob(filepath.Join(BackupDir(b.Home), "*", "library", "agents", "helper.md"))
	found := false
	for _, p := range baks {
		if c, _ := os.ReadFile(p); strings.Contains(string(c), "B says hi.") {
			found = true
		}
	}
	if !found {
		t.Fatal("b's conflicting edit was not backed up")
	}
}

func TestLibraryPushBlocksSecrets(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	libWrite(t, a, "skills/x/SKILL.md", "---\nname: x\n---\napi_key = \"abcd1234efgh5678ijkl\"\n")
	var se *SecretsError
	if _, err := a.LibPush(false); !errors.As(err, &se) || len(se.Findings) != 1 {
		t.Fatalf("secret not blocked: %v", err)
	}
	libWrite(t, a, "skills/x/SKILL.md", "---\nname: x\n---\napi_key = \"${MY_API_KEY}\"\ntoken: <your token here>\n")
	if _, err := a.LibPush(false); err != nil {
		t.Fatalf("placeholder blocked: %v", err)
	}
}

// A new machine starts with the default agents; the pulled agents.json
// must decide where the library is rendered, not the defaults.
func TestNewMachineRendersOnlySyncedAgents(t *testing.T) {
	en := newEnv(t)
	codex, claude := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("HEARTH_AGENTS", "")
	a := en.machine("a")
	libWrite(t, a, "skills/s1/SKILL.md", "---\nname: s1\ndescription: d\n---\nx\n")
	libWrite(t, a, "agents.json", `{"enabled":["codex"]}`)
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}
	b := en.machine("b")
	if len(b.LibTargets) != 2 {
		t.Fatalf("new machine should start with default targets, got %d", len(b.LibTargets))
	}
	if _, err := b.LibPull(false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Render(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(codex, "skills/s1/SKILL.md")); err != nil {
		t.Fatal("codex did not get the skill")
	}
	if _, err := os.Stat(filepath.Join(claude, "skills/s1")); err == nil {
		t.Fatal("claude got the skill although only codex is enabled")
	}
}

// Machines set up before the fix got the library in every default agent;
// the next render removes the copies of disabled agents.
func TestDisabledAgentCopiesAreRemoved(t *testing.T) {
	en := newEnv(t)
	codex, claude := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("HEARTH_AGENTS", "")
	a := en.machine("a")
	libWrite(t, a, "skills/s1/SKILL.md", "---\nname: s1\ndescription: d\n---\nx\n")
	libWrite(t, a, "agents.json", `{"enabled":["codex"]}`)
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}
	b := en.machine("b")
	both := b.LibTargets
	if _, err := b.LibPull(false); err != nil {
		t.Fatal(err)
	}
	b.LibTargets, b.autoTargets = both, nil // the old behaviour
	if _, err := b.Render(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claude, "skills/s1/SKILL.md")); err != nil {
		t.Fatal("setup: claude copy missing")
	}
	b.LibTargets = EnabledLibTargets(b.Home)
	b.autoTargets = b.LibTargets
	if _, err := b.Render(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claude, "skills/s1")); err == nil {
		t.Fatal("claude copy left behind")
	}
	if _, err := os.Stat(filepath.Join(codex, "skills/s1/SKILL.md")); err != nil {
		t.Fatal("codex copy removed")
	}
}

// A conflict stops recipes and credentials from syncing. The daemon logs it
// once, so keep the reason on disk for `hearth doctor` until a sync works.
func TestLibraryConflictIsRemembered(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	libWrite(t, a, "skills/s1/SKILL.md", "---\nname: s1\ndescription: d\n---\nx\n")
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.LibPull(false); err != nil {
		t.Fatal(err)
	}
	libWrite(t, a, "skills/local/SKILL.md", "---\nname: l\ndescription: d\n---\nx\n")
	libWrite(t, b, "skills/remote/SKILL.md", "---\nname: r\ndescription: d\n---\nx\n")
	if _, err := b.LibPush(false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.LibAuto(); !errors.Is(err, ErrLibraryConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if msg, _, ok := LibraryError(a.Home); !ok || !strings.Contains(msg, "library changed both locally") {
		t.Fatalf("conflict not remembered: %q %v", msg, ok)
	}
	if _, err := a.LibPull(true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.LibAuto(); err != nil {
		t.Fatal(err)
	}
	if msg, _, ok := LibraryError(a.Home); ok {
		t.Fatalf("stale conflict kept: %q", msg)
	}
}

// Moving the skills store keeps one copy, rewrites the agent links and is
// saved in the library so other machines inherit the setting.
func TestSetSkillsDirMovesAndRelinks(t *testing.T) {
	en := newEnv(t)
	codex := t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	a := en.machine("a")
	libWrite(t, a, "skills/s1/SKILL.md", "---\nname: s1\ndescription: d\n---\nx\n")
	if _, err := a.Render(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(codex, "skills/s1/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	ext := filepath.Join(t.TempDir(), "skills")
	got, err := SetSkillsDir(a.Home, ext)
	if err != nil {
		t.Fatal(err)
	}
	if got != ext {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(ext, "s1", "SKILL.md")); err != nil {
		t.Fatal("skills not moved")
	}
	if _, err := os.Stat(filepath.Join(LibraryDir(a.Home), "skills", "s1")); err == nil {
		t.Fatal("skills left in the old place")
	}
	cfg, _ := library.Library{Root: LibraryDir(a.Home)}.LoadConfig()
	if cfg.Skills.Dir != ext {
		t.Fatalf("config %q", cfg.Skills.Dir)
	}
	if _, err := a.Render(false); err != nil {
		t.Fatal(err)
	}
	if link, _ := os.Readlink(filepath.Join(codex, "skills/s1")); link != filepath.Join(ext, "s1") {
		t.Fatalf("link %q", link)
	}
}

// A pulled config.toml sends the bundle's skills to the configured path.
func TestPullHonorsSkillsDir(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	libWrite(t, a, "skills/s1/SKILL.md", "---\nname: s1\ndescription: d\n---\nx\n")
	extA := filepath.Join(t.TempDir(), "ext")
	if _, err := SetSkillsDir(a.Home, extA); err != nil {
		t.Fatal(err)
	}
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}
	b := en.machine("b")
	if _, err := b.LibPull(false); err != nil {
		t.Fatal(err)
	}
	cfg, _ := library.Library{Root: LibraryDir(b.Home)}.LoadConfig()
	if cfg.Skills.Dir != extA {
		t.Fatalf("pulled config %q", cfg.Skills.Dir)
	}
	if _, err := os.Stat(filepath.Join(extA, "s1", "SKILL.md")); err != nil {
		t.Fatalf("skills not at configured dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(LibraryDir(b.Home), "skills", "s1")); err == nil {
		t.Fatal("skills also left in the library")
	}
}
