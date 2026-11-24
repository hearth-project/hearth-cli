package client

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// adoptTeamPackage replaces this machine's copy with the team's newest
// version. Credential files the team package does not contain are kept:
// they belong to this user, not to the shared package.
func (e *Engine) adoptTeamPackage(team, typ, name, wantSum string) (int64, error) {
	data, version, sum, err := e.API.GetPackage(team, typ, name, 0)
	if err != nil {
		return 0, err
	}
	if wantSum != "" && sum != wantSum {
		return 0, fmt.Errorf("team %s %s %s changed while syncing (now v%d); it will be picked up next time", team, typ, name, version)
	}
	tmpBase, err := os.MkdirTemp(e.Home, "share-adopt-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmpBase)
	tmp := filepath.Join(tmpBase, "x")
	got, err := library.Extract(data, tmp)
	if err != nil {
		return 0, err
	}
	if got != sum {
		return 0, fmt.Errorf("team %s %s %s: content checksum mismatch", team, typ, name)
	}
	lib := library.Library{Root: LibraryDir(e.Home)}
	switch typ {
	case "skill":
		if err := syncSkillTree(tmp, filepath.Join(lib.SkillsDir(), name)); err != nil {
			return 0, err
		}
	case "agent":
		b, err := os.ReadFile(filepath.Join(tmp, "AGENT.md"))
		if err != nil {
			return 0, err
		}
		if err := os.MkdirAll(lib.AgentsDir(), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(filepath.Join(lib.AgentsDir(), name+".md"), b, 0o644); err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("unknown package type %q", typ)
	}
	return version, nil
}

// syncSkillTree copies the team's files over the local skill and removes
// files that no longer exist in the team copy. Local credential files are
// kept: they are intentionally absent from the shared package.
func syncSkillTree(src, dst string) error {
	if _, err := library.CopyTree(src, dst); err != nil {
		return err
	}
	expected := map[string]bool{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		expected[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		return err
	}
	credentials := map[string]bool{}
	if fs, err := library.ScanSecrets(dst); err == nil {
		for _, f := range fs {
			credentials[filepath.ToSlash(f.File)] = true
		}
	}
	return filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dst && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil // editor and VCS state is not part of the shared skill
		}
		rel, _ := filepath.Rel(dst, p)
		rel = filepath.ToSlash(rel)
		if expected[rel] || credentials[rel] {
			return nil
		}
		return os.Remove(p)
	})
}
