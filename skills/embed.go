// Package skills holds the agent skill that ships with Hearth. It is
// embedded in the binary: the installer, `hearth setup`, `hearth
// self-update` and `hearth onboard install-skill` install it into every
// coding agent on the machine, and the server serves it at
// /onboard/SKILL.md so an agent on a machine without Hearth can follow it.
package skills

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

// Hearth is skills/hearth/SKILL.md.
//
//go:embed hearth/SKILL.md
var Hearth string

//go:embed */SKILL.md
var files embed.FS

// Marker ends every SKILL.md Hearth installs: such copies are updated in
// place, never imported into a user's library, and a user's own skill with
// the same name is left alone.
const Marker = "<!-- installed by hearth; updated by hearth self-update / hearth onboard install-skill -->"

// Bundled reports whether a SKILL.md is a copy Hearth installed (including
// hearth-setup copies from before the marker existed).
func Bundled(skillMD []byte) bool {
	s := string(skillMD)
	return strings.Contains(s, Marker) || strings.Contains(s, "\n# Hearth setup\n")
}

// Retired are skill names earlier versions installed; Hearth removes its
// own copies of them.
var Retired = []string{"hearth-setup"}

// Skill is one bundled skill.
type Skill struct {
	Name    string // directory name
	Content string // SKILL.md
}

// All returns the bundled skills, sorted by name.
func All() []Skill {
	var out []Skill
	matches, _ := fs.Glob(files, "*/SKILL.md")
	for _, m := range matches {
		b, err := files.ReadFile(m)
		if err != nil {
			continue
		}
		out = append(out, Skill{Name: m[:len(m)-len("/SKILL.md")], Content: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
