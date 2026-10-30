package client

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/api"
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

	// Concurrent edits of the same file: the second machine merges, keeps
	// its own copy and backs up the server's; nothing is lost.
	libWrite(t, a, "agents/helper.md", strings.Replace(agentMD, "Be helpful.", "A says hi.", 1))
	libWrite(t, b, "agents/helper.md", strings.Replace(agentMD, "Be helpful.", "B says hi.", 1))
	if _, err := a.LibAuto(); err != nil {
		t.Fatal(err)
	}
	if r, err := b.LibAuto(); err != nil || r.Op != "merged" {
		t.Fatalf("want a merge, got %+v %v", r, err)
	}
	if c, _ := os.ReadFile(filepath.Join(LibraryDir(b.Home), "agents/helper.md")); !strings.Contains(string(c), "B says hi.") {
		t.Fatalf("b lost its edit:\n%s", c)
	}
	baks, _ := filepath.Glob(filepath.Join(BackupDir(b.Home), "*", "merge-server-copies", "agents", "helper.md"))
	found := false
	for _, p := range baks {
		if c, _ := os.ReadFile(p); strings.Contains(string(c), "A says hi.") {
			found = true
		}
	}
	if !found {
		t.Fatal("the server's conflicting copy was not backed up")
	}
	if _, err := a.LibAuto(); err != nil {
		t.Fatal(err)
	}
	if c, _ := os.ReadFile(filepath.Join(LibraryDir(a.Home), "agents/helper.md")); !strings.Contains(string(c), "B says hi.") {
		t.Fatalf("merge not on the server:\n%s", c)
	}
}

// Credentials outside the skills block the upload; inside a skill they
// travel as encrypted secrets, not in the plaintext bundle, and come back
// into the skill on the next machine.
func TestLibraryCredentialsTravelEncrypted(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	libWrite(t, a, "recipes/x.toml", "name = \"x\"\napi_key = \"abcd1234efgh5678ijkl\"\n")
	var se *SecretsError
	if _, err := a.LibPush(false); !errors.As(err, &se) || len(se.Findings) != 1 {
		t.Fatalf("secret in a recipe not blocked: %v", err)
	}
	os.Remove(filepath.Join(LibraryDir(a.Home), "recipes/x.toml"))
	libWrite(t, a, "skills/bos/SKILL.md", "---\nname: bos\ndescription: d\n---\nupload\n")
	libWrite(t, a, "skills/bos/assets/bos_config/credentials", "[Defaults]\nAk = 0123456789abcdef0123\nSk = fedcba9876543210fedc\n")
	libWrite(t, a, "skills/bos/assets/private.conf", "anything\n")
	os.Chmod(filepath.Join(LibraryDir(a.Home), "skills/bos/assets/private.conf"), 0o600)
	libWrite(t, a, "skills/doc/SKILL.md", "---\nname: doc\ndescription: d\n---\napi_key = \"${MY_API_KEY}\"\ntoken: <your token here>\n")
	if _, err := a.LibPush(false); err != nil {
		t.Fatalf("push: %v", err)
	}
	data, _, _, err := a.API.GetLibrary(-1)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "x")
	if _, err := library.Extract(data, dir); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(dir, "skills/bos/assets/bos_config/credentials")) || fileExists(filepath.Join(dir, "skills/bos/assets/private.conf")) {
		t.Fatal("credential files in the plaintext bundle")
	}
	if !fileExists(filepath.Join(dir, "skills/bos/SKILL.md")) || !fileExists(filepath.Join(dir, "skills/doc/SKILL.md")) {
		t.Fatal("skill content missing from the bundle")
	}
	if _, err := b.LibPull(false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(LibraryDir(b.Home), "skills/bos/assets/bos_config/credentials")
	if c, err := os.ReadFile(p); err != nil || !strings.Contains(string(c), "Ak = 0123") {
		t.Fatalf("credentials not restored on b: %v", err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	// A changed credential alone is pushed too; a removed one is deleted.
	libWrite(t, a, "skills/bos/assets/bos_config/credentials", "[Defaults]\nAk = 1111111111abcdef0123\nSk = fedcba9876543210fedc\n")
	if r, err := a.LibPush(false); err != nil || r.Op != "pushed" {
		t.Fatalf("credential-only push: %+v %v", r, err)
	}
	os.RemoveAll(filepath.Join(LibraryDir(a.Home), "skills/bos"))
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}
	ss, _ := a.Secrets()
	for _, s := range ss {
		if strings.HasPrefix(s.RestoreTo, libRestorePrefix) {
			t.Fatalf("secret of a deleted skill kept: %s", s.Name)
		}
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

// Both sides changed: the daemon merges instead of stopping the sync, and
// nothing is reported as an error afterwards.
func TestLibraryConflictMerges(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	libWrite(t, a, "skills/s1/SKILL.md", "---\nname: s1\ndescription: d\n---\nx\n")
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.LibPull(false); err != nil {
		t.Fatal(err)
	}
	libWrite(t, a, "skills/local/SKILL.md", "---\nname: local\ndescription: d\n---\nx\n")
	libWrite(t, b, "skills/remote/SKILL.md", "---\nname: remote\ndescription: d\n---\nx\n")
	os.RemoveAll(filepath.Join(LibraryDir(b.Home), "skills/s1"))
	if _, err := b.LibPush(false); err != nil {
		t.Fatal(err)
	}
	if r, err := a.LibAuto(); err != nil || r.Op != "merged" {
		t.Fatalf("want a merge, got %+v %v", r, err)
	}
	for _, sk := range []string{"local", "remote"} {
		if !fileExists(filepath.Join(LibraryDir(a.Home), "skills", sk, "SKILL.md")) {
			t.Fatalf("%s missing after the merge", sk)
		}
	}
	if fileExists(filepath.Join(LibraryDir(a.Home), "skills/s1")) {
		t.Fatal("deleted on b, unchanged on a: the merge keeps the deletion")
	}
	if msg, _, ok := LibraryError(a.Home); ok {
		t.Fatalf("error kept after a merge: %q", msg)
	}
	if _, err := b.LibAuto(); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(LibraryDir(b.Home), "skills/local/SKILL.md")) {
		t.Fatal("merge not pushed")
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

// A library larger than one session chunk (a skill shipping a CLI binary)
// downloads whole; it was cut off and failed as a corrupt bundle.
func TestLargeLibraryPulls(t *testing.T) {
	en := newEnv(t)
	a, b := en.machine("a"), en.machine("b")
	libWrite(t, a, "skills/big/SKILL.md", "---\nname: big\ndescription: d\n---\nx\n")
	blob := make([]byte, api.MaxChunkBytes+(2<<20))
	rand.Read(blob) // incompressible
	p := filepath.Join(LibraryDir(a.Home), "skills/big/assets/tool")
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, blob, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}
	if r, err := b.LibPull(false); err != nil || r.Op != "pulled" {
		t.Fatalf("pull: %+v %v", r, err)
	}
	if fi, err := os.Stat(filepath.Join(LibraryDir(b.Home), "skills/big/assets/tool")); err != nil || fi.Size() != int64(len(blob)) {
		t.Fatalf("tool not pulled whole: %v", err)
	}
}
