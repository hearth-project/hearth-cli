package client

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// Credentials a skill keeps outside its directory (an auth file in
// ~/.config, a token cache) are declared in its SKILL.md front matter
// (metadata.hearth.credentials, see library.SkillCredentials). Learning
// records each file found as an encrypted secret named after the skill
// (skill.<skill>.<file>), so `hearth secret use <skill>` and the MCP tool
// prepare_credentials write it back to its path on any machine.

const skillSecretPrefix = "skill."

// skillSecretName names the secret of one declared file of skill.
func skillSecretName(skill, p string) string {
	n := strings.TrimPrefix(secretNameFor(p), "file-")
	name := skillSecretPrefix + skill + "." + n
	if len(name) > 128 {
		name = name[:128]
	}
	return name
}

// skillOfSecret is the skill a skill.<skill>.<file> secret belongs to.
func skillOfSecret(name string) string {
	rest, ok := strings.CutPrefix(name, skillSecretPrefix)
	if !ok {
		return ""
	}
	s, _, _ := strings.Cut(rest, ".")
	return s
}

var hearthUserRE = regexp.MustCompile(`\$\{HEARTH_USER\}`)

// expandSkillCredential turns a declared path into a pattern on this
// machine; "" when it is not a safe, portable path.
func (e *Engine) expandSkillCredential(p string) string {
	if strings.Contains(p, "${HEARTH_USER}") {
		user, _, _ := strings.Cut(e.Cfg.Email, "@")
		if user == "" {
			return ""
		}
		p = hearthUserRE.ReplaceAllString(p, user)
	}
	if !paths.Portable(p) {
		return ""
	}
	return paths.Expand(p)
}

// SkillCredentialDecl is one skill's declared credential files.
type SkillCredentialDecl struct {
	Skill string
	Paths []string // as declared
}

// SkillCredentialDecls lists the skills of the library that declare
// credential files.
func (e *Engine) SkillCredentialDecls() []SkillCredentialDecl {
	c, err := (library.Library{Root: LibraryDir(e.Home)}).Load()
	if err != nil {
		return nil
	}
	var out []SkillCredentialDecl
	for _, s := range c.Skills {
		b, err := os.ReadFile(filepath.Join(s.Dir, "SKILL.md"))
		if err != nil {
			continue
		}
		if ps := library.SkillCredentials(b); len(ps) > 0 {
			out = append(out, SkillCredentialDecl{Skill: s.Name, Paths: ps})
		}
	}
	return out
}

// skillCredentialFindings offers every declared file present here.
func (e *Engine) skillCredentialFindings() []Finding {
	var out []Finding
	for _, d := range e.SkillCredentialDecls() {
		for _, decl := range d.Paths {
			pat := e.expandSkillCredential(decl)
			if pat == "" {
				continue
			}
			matches, _ := filepath.Glob(pat)
			for _, m := range matches {
				if fi, err := os.Stat(m); err != nil || !fi.Mode().IsRegular() {
					continue
				}
				f := fileSecret("skill-cred:"+d.Skill+":"+paths.Contract(m), "credential",
					"credential of skill "+d.Skill+": "+filepath.Base(m), m, "if-absent")
				f.secretName = skillSecretName(d.Skill, m)
				for i := range f.steps {
					f.steps[i].Secret = f.secretName
				}
				out = append(out, f)
			}
		}
	}
	return out
}

// missingSkillCredentials explains, for a skill that declares credential
// files none of which is stored, what to do; "" when it declares none.
func (e *Engine) missingSkillCredentials(skill string) string {
	for _, d := range e.SkillCredentialDecls() {
		if d.Skill == skill {
			return fmt.Sprintf("skill %s needs %s, not recorded from any machine yet: on a machine where it works, hearth learn records it (the background sync does within 20 minutes)",
				skill, strings.Join(d.Paths, ", "))
		}
	}
	return ""
}
