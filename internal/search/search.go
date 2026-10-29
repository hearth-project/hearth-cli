// Package search implements on-demand skill retrieval (design doc section
// 9): a small BM25 index over skill names, descriptions and bodies with a
// tokenizer that handles mixed Chinese/English text without a dictionary.
package search

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Skill is one indexed skill.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`        // e.g. "library", "project"
	Dir         string `json:"dir"`           // "" for remote skills not downloaded yet
	Ref         string `json:"ref,omitempty"` // remote: "source:name"
	body        string
}

// Result is one search hit.
type Result struct {
	Skill
	Score float64 `json:"score"`
}

// Tokens is the tokenizer of the skill search, for other indexes (memory)
// that must split mixed Chinese / English text the same way.
func Tokens(s string) []string { return tokenize(s) }

// tokenize lower-cases text, keeps latin/digit words (also split on - _ /),
// and turns runs of CJK characters into overlapping bigrams.
func tokenize(s string) []string {
	var out []string
	var word []rune
	var cjk []rune
	flushWord := func() {
		if len(word) >= 2 {
			out = append(out, stem(string(word)))
		}
		word = word[:0]
	}
	flushCJK := func() {
		switch {
		case len(cjk) == 1:
			out = append(out, string(cjk))
		case len(cjk) > 1:
			for i := 0; i+1 < len(cjk); i++ {
				out = append(out, string(cjk[i:i+2]))
			}
		}
		cjk = cjk[:0]
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			word = append(word, r)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return out
}

// stem strips common English suffixes so "warnings", "warning" and "warned"
// meet; it is deliberately crude but symmetric for index and query.
func stem(w string) string {
	// Plurals first, then verb endings, so "warnings" -> "warning" -> "warn".
	switch {
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		w = w[:len(w)-3] + "y"
	case len(w) > 4 && strings.HasSuffix(w, "es") && !strings.HasSuffix(w, "ses"):
		w = w[:len(w)-2]
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		w = w[:len(w)-1]
	}
	for _, suf := range []string{"ing", "ed"} {
		if len(w) > len(suf)+2 && strings.HasSuffix(w, suf) {
			return w[:len(w)-len(suf)]
		}
	}
	return w
}

// Index is an in-memory BM25 index. Skill libraries are small (hundreds to
// a few thousand entries), so rebuilding it is cheap.
type Index struct {
	skills []Skill
	length []float32
	post   map[string][]posting // term -> documents containing it
	byName map[string]int
	names  []string // lower-cased names, for the verbatim-mention boost
	avgLen float32
}

type posting struct {
	doc int32
	tf  float32
}

// Field weights: a term in the name counts four times a body term.
const (
	wName = 4.0
	wDesc = 3.0
	wBody = 1.0
	// Only the start of a SKILL.md body is indexed; long reference sections
	// add noise rather than signal.
	bodyChars = 4000
)

var nameSplitter = strings.NewReplacer("-", " ", "_", " ", ":", " ")

// Build indexes the given skills into an inverted index, so a query only
// visits documents that contain at least one of its terms.
func Build(skills []Skill) *Index {
	ix := &Index{skills: skills, post: map[string][]posting{}, byName: make(map[string]int, len(skills)),
		length: make([]float32, len(skills)), names: make([]string, len(skills))}
	var total float32
	tf := map[string]float32{}
	for i, s := range skills {
		clear(tf)
		add := func(text string, w float32) {
			for _, t := range tokenize(text) {
				tf[t] += w
			}
		}
		add(nameSplitter.Replace(s.Name), wName)
		add(s.Description, wDesc)
		body := s.body
		if len(body) > bodyChars*3 { // cheap byte cut before the rune cut
			body = body[:bodyChars*3]
		}
		if r := []rune(body); len(r) > bodyChars {
			body = string(r[:bodyChars])
		}
		add(body, wBody)
		var l float32
		for t, v := range tf {
			ix.post[t] = append(ix.post[t], posting{int32(i), v})
			l += v
		}
		ix.length[i] = l
		total += l
		if _, dup := ix.byName[s.Name]; !dup {
			ix.byName[s.Name] = i
		}
		ix.names[i] = strings.ToLower(s.Name)
	}
	if len(skills) > 0 {
		ix.avgLen = total / float32(len(skills))
	}
	return ix
}

// Len returns the number of indexed skills.
func (ix *Index) Len() int { return len(ix.skills) }

// Skills returns every indexed skill sorted by name.
func (ix *Index) Skills() []Skill {
	out := append([]Skill(nil), ix.skills...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get finds a skill by exact name.
func (ix *Index) Get(name string) (Skill, bool) {
	if i, ok := ix.byName[name]; ok {
		return ix.skills[i], true
	}
	return Skill{}, false
}

// Search ranks skills for query with BM25 (k1=1.2, b=0.75) plus a boost
// when the query mentions a skill name verbatim. It only touches documents
// that share a term with the query, accumulating into a dense score array.
func (ix *Index) Search(query string, limit int) []Result {
	const k1, b = 1.2, 0.75
	n := float64(len(ix.skills))
	if n == 0 {
		return nil
	}
	scores := make([]float64, len(ix.skills))
	var touched []int32
	seen := map[string]bool{}
	for _, t := range tokenize(query) {
		if seen[t] {
			continue
		}
		seen[t] = true
		ps := ix.post[t]
		if len(ps) == 0 {
			continue
		}
		df := float64(len(ps))
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		norm := k1 * (1 - b)
		scale := k1 * b / float64(ix.avgLen)
		for _, p := range ps {
			if scores[p.doc] == 0 {
				touched = append(touched, p.doc)
			}
			f := float64(p.tf)
			scores[p.doc] += idf * f * (k1 + 1) / (f + norm + scale*float64(ix.length[p.doc]))
		}
	}
	lq := strings.ToLower(query)
	for _, doc := range touched {
		if strings.Contains(lq, ix.names[doc]) {
			scores[doc] += 5
		}
	}
	for _, w := range strings.Fields(lq) {
		if i, ok := ix.byName[w]; ok && scores[i] == 0 {
			scores[i] = 5
			touched = append(touched, int32(i))
		}
	}
	less := func(x, y int32) bool { // x ranks before y
		if scores[x] != scores[y] {
			return scores[x] > scores[y]
		}
		return ix.skills[x].Name < ix.skills[y].Name
	}
	if limit <= 0 || limit > len(touched) {
		limit = len(touched)
	}
	// Partial selection: keep the best `limit` documents without sorting all.
	top := make([]int32, 0, limit)
	for _, d := range touched {
		if len(top) < limit {
			top = append(top, d)
			for i := len(top) - 1; i > 0 && less(top[i], top[i-1]); i-- {
				top[i], top[i-1] = top[i-1], top[i]
			}
		} else if less(d, top[limit-1]) {
			top[limit-1] = d
			for i := limit - 1; i > 0 && less(top[i], top[i-1]); i-- {
				top[i], top[i-1] = top[i-1], top[i]
			}
		}
	}
	res := make([]Result, len(top))
	for i, d := range top {
		res[i] = Result{Skill: ix.skills[d], Score: math.Round(scores[d]*100) / 100}
	}
	return res
}

// LoadDir reads every <dir>/<name>/SKILL.md. Front matter name/description
// are used when present; the directory name is the fallback name.
func LoadDir(dir, source string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		skillDir := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
		if err != nil {
			continue
		}
		s := Skill{Name: e.Name(), Source: source, Dir: skillDir}
		s.Description, s.body = parseSkillMD(string(data))
		out = append(out, s)
	}
	return out, nil
}

// NewSkill builds an indexable skill from its parts.
func NewSkill(name, description, body, source string) Skill {
	return Skill{Name: name, Description: description, Source: source, body: body}
}

// SplitSkillMD returns the description and body of a SKILL.md.
func SplitSkillMD(data []byte) (desc, body string) { return parseSkillMD(string(data)) }

// LoadSkill reads one skill directory.
func LoadSkill(dir, name, source string) (Skill, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return Skill{}, err
	}
	s := Skill{Name: name, Source: source, Dir: dir}
	s.Description, s.body = parseSkillMD(string(data))
	return s, nil
}

func parseSkillMD(s string) (desc, body string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return "", s
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return "", s
	}
	var fm struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(s[4:4+end]), &fm); err != nil {
		// Hand-written front matter is often not strict YAML (for example an
		// unquoted ": " inside the description); fall back to the raw line.
		for _, line := range strings.Split(s[4:4+end], "\n") {
			if v, ok := strings.CutPrefix(line, "description:"); ok {
				fm.Description = strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
	}
	rest := s[4+end+4:]
	return strings.TrimSpace(fm.Description), strings.TrimLeft(rest, "\n")
}
