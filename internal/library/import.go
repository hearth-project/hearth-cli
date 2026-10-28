package library

import (
	"github.com/hearth-project/hearth-cli/skills"

	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Import copies a target's existing skills, agents and instructions into
// the library, converting agents to the canonical format. Paths rendered by
// hearth (in the manifest or symlinked into the library) are skipped.
func Import(lib Library, t Target, manifestPath string, overwrite bool) ([]Action, error) {
	m, err := loadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	managed := func(p string) bool {
		if _, ok := m.Entries[p]; ok {
			return true
		}
		if dst, err := os.Readlink(p); err == nil && strings.HasPrefix(filepath.Clean(dst), filepath.Clean(lib.Root)+string(filepath.Separator)) {
			return true
		}
		return false
	}
	var acts []Action
	skip := func(p, item, why string) { acts = append(acts, Action{Path: p, Item: item, Op: "skip", Detail: why}) }

	// Skills
	entries, err := os.ReadDir(filepath.Join(t.Root, "skills"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		name, src := e.Name(), filepath.Join(t.Root, "skills", e.Name())
		item := "skill:" + name
		switch {
		case strings.HasPrefix(name, "."):
			continue // e.g. .system skills shipped by the agent itself
		case managed(src):
			continue
		case !ValidName(name):
			skip(src, item, "invalid name")
			continue
		}
		md, err := os.ReadFile(filepath.Join(src, "SKILL.md"))
		if err != nil {
			skip(src, item, "no SKILL.md")
			continue
		}
		if skills.Bundled(md) {
			continue // ships with Hearth; installed on every machine anyway
		}
		// A skill entry may itself be a symlink to a shared directory (e.g.
		// .codex/skills/x -> ../../.comate/skills/x): copy the real directory.
		real, err := filepath.EvalSymlinks(src)
		if err != nil {
			skip(src, item, err.Error())
			continue
		}
		dst := filepath.Join(lib.SkillsDir(), name)
		if _, err := os.Stat(dst); err == nil {
			if !overwrite {
				skip(src, item, "already in library (use --overwrite)")
				continue
			}
			if err := os.RemoveAll(dst); err != nil {
				return acts, err
			}
		}
		warns, err := CopyTree(real, dst)
		if err != nil {
			return acts, fmt.Errorf("copy %s: %w", src, err)
		}
		detail := ""
		if real != src {
			detail = "from " + real
		}
		acts = append(acts, Action{Path: src, Item: item, Op: "import", Detail: detail})
		for _, w := range warns {
			acts = append(acts, Action{Path: w, Item: item, Op: "warn", Detail: "symlink not copied"})
		}
	}

	if t.SkillsOnly() {
		return acts, nil
	}
	// Agents
	entries, err = os.ReadDir(filepath.Join(t.Root, "agents"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		src := filepath.Join(t.Root, "agents", e.Name())
		if e.IsDir() {
			acts = append(acts, Action{Path: src, Op: "warn", Detail: "directory next to agent definitions not imported (copy companion files manually)"})
			continue
		}
		if !strings.HasSuffix(e.Name(), t.agentExt) || managed(src) {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return acts, err
		}
		var a *Agent
		if t.Name == "codex" {
			a, err = ParseCodexAgent(data)
		} else {
			a, err = ParseClaudeAgent(data)
		}
		if err != nil {
			skip(src, "agent", err.Error())
			continue
		}
		item := "agent:" + a.Name
		dst := filepath.Join(lib.AgentsDir(), a.Name+".md")
		if _, err := os.Stat(dst); err == nil && !overwrite {
			skip(src, item, "already in library (use --overwrite)")
			continue
		}
		out, err := a.Format()
		if err != nil {
			return acts, err
		}
		if err := writeFile(dst, out); err != nil {
			return acts, err
		}
		acts = append(acts, Action{Path: src, Item: item, Op: "import"})
	}

	// Instructions
	src := t.instrPath()
	if data, err := os.ReadFile(src); t.instrFile != "" && err == nil && !managed(src) {
		dst := lib.InstructionsFile()
		if _, err := os.Stat(dst); err == nil && !overwrite {
			skip(src, "instructions", "already in library (use --overwrite)")
		} else {
			if err := writeFile(dst, data); err != nil {
				return acts, err
			}
			acts = append(acts, Action{Path: src, Item: "instructions", Op: "import"})
		}
	}
	return acts, nil
}

func writeFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// CopyTree copies regular files and directories; symlinks are reported, not
// followed, so a skill cannot pull files from outside its directory.
func CopyTree(src, dst string) (skipped []string, err error) {
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			skipped = append(skipped, p)
			return nil
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()|0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	return skipped, err
}
