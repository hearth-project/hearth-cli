package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// The user keeps personal agent state in the workspace (project .codex /
// .comate / .claude, memory graph, ssh files), as in a workspace that is
// also the Codex project directory. learn finds it; it syncs encrypted and
// comes back into the workspace of a machine with a different layout.
func TestWorkspaceAgentDirs(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	home, ws := learnEnv(t)
	w := func(rel, data string) {
		p := filepath.Join(ws, rel)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(data), 0o600)
	}
	w(".codex/skills/bench/SKILL.md", "---\nname: bench\ndescription: load test\n---\nrun it\n")
	w(".codex/agents/reviewer.toml", "name = \"reviewer\"\ndescription = \"reviews\"\ndeveloper_instructions = \"be strict\"\n")
	w(".comate/skills/icafe/SKILL.md", "---\nname: icafe\ndescription: cards\n---\nx\n")
	w(".comate/memory/MEMORY.md", "- likes short answers\n")
	w(".comate/memory/project_gds.md", "gds notes\n")
	w(".comate/mcp.json", `{"mcpServers":{"ku":{"command":"npx","args":["ku-mcp"],"env":{"KU_TOKEN":"ku_SECRETTOKEN123456"}}}}`)
	w(".claude/settings.local.json", `{"permissions":{"allow":["Bash(ls:*)"]}}`)
	w(".memory/graph.json", `{"entities":[]}`)
	w(".ssh/known_hosts", "gitsrv ssh-rsa AAA\n")
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nWS\n-----END OPENSSH PRIVATE KEY-----\n"
	w(".ssh/id_rsa", key)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh/id_rsa"), []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nHOME\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600)

	fs := findingIDs(a.Discover())
	for _, id := range []string{"lib:${WORKSPACE}/.codex", "lib:${WORKSPACE}/.comate", "wsfile:.comate/memory", "wsfile:.memory",
		"wsfile:.claude/settings.local.json", "wsfile:.ssh/known_hosts", "mcp:ws-comate"} {
		if _, ok := fs[id]; !ok {
			t.Fatalf("missing %s; have %v", id, keys(fs))
		}
	}
	if !strings.Contains(fs["lib:${WORKSPACE}/.codex"].Detail, "bench") || !strings.Contains(fs["lib:${WORKSPACE}/.codex"].Detail, "reviewer") {
		t.Fatalf("codex lib detail %q", fs["lib:${WORKSPACE}/.codex"].Detail)
	}
	// Two different keys named id_rsa get two secrets.
	wsKey, homeKey := fs["ssh-key:${WORKSPACE}/.ssh/id_rsa"], fs["ssh-key:~/.ssh/id_rsa"]
	if wsKey.secretName == homeKey.secretName {
		t.Fatalf("both keys stored as %s", wsKey.secretName)
	}
	learned, _ := LoadLearned(a.Home)
	for _, f := range fs {
		if _, err := a.Accept(f, learned); err != nil {
			t.Fatalf("%s: %v", f.ID, err)
		}
	}
	SaveRecipe(a.Home, learned)
	for _, p := range []string{"skills/bench/SKILL.md", "skills/icafe/SKILL.md", "agents/reviewer.md"} {
		if _, err := os.Stat(filepath.Join(LibraryDir(a.Home), p)); err != nil {
			t.Fatalf("library lacks %s", p)
		}
	}
	if again := findingIDs(a.Discover()); again["lib:${WORKSPACE}/.codex"].ID != "" || again["wsfile:.comate/memory"].ID != "" {
		t.Fatal("accepted items are found again")
	}
	a.Adapters = append(a.Adapters, workspaceAdapter(a.Home))
	a.push(t)
	if _, err := a.LibPush(false); err != nil {
		t.Fatal(err)
	}
	all, _ := a.List()
	for _, rs := range all {
		if rs.Meta.Agent == "workspace" && !rs.Pinned {
			t.Fatalf("workspace file %s not pinned", rs.Meta.RelPath)
		}
	}

	a.ReleaseLeases()
	// Machine B: different home and workspace; library first, then sessions.
	b := en.machine("b")
	_, ws2 := learnEnv(t)
	if _, err := b.LibPull(false); err != nil {
		t.Fatal(err)
	}
	b.Adapters = append(b.Adapters, workspaceAdapter(b.Home))
	all, _ = b.List()
	for _, rs := range all {
		if _, err := b.Restore(rs, false); err != nil {
			t.Fatal(err)
		}
	}
	for rel, want := range map[string]string{".comate/memory/project_gds.md": "gds notes\n", ".memory/graph.json": `{"entities":[]}`,
		".ssh/known_hosts": "gitsrv ssh-rsa AAA\n", ".claude/settings.local.json": `{"permissions":{"allow":["Bash(ls:*)"]}}`} {
		if got, _ := os.ReadFile(filepath.Join(ws2, rel)); string(got) != want {
			t.Fatalf("%s on B: %q", rel, got)
		}
	}
	if res, _ := b.RunRecipes([]string{LearnedRecipe}, recipe.Options{Mode: recipe.Apply}); len(res) == 0 {
		t.Fatal("no recipe results")
	}
	if got, _ := os.ReadFile(filepath.Join(ws2, ".ssh/id_rsa")); string(got) != key {
		t.Fatalf("workspace key on B: %q", got)
	}
	// Edits on B flow back like any other file.
	os.WriteFile(filepath.Join(ws2, ".comate/memory/MEMORY.md"), []byte("- likes short answers\n- uses H800\n"), 0o600)
	if st, err := b.PushOnce(); err != nil || st.Bytes == 0 {
		t.Fatalf("push from B: %+v %v", st, err)
	}
	var _ adapter.Adapter = workspaceAdapter(b.Home)
}

func keys(m map[string]Finding) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
