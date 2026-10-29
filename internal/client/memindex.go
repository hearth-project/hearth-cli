package client

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/memory"
)

// Long-term memory, built on the synced data (design: docs memory roadmap):
//   - conversation cards of every machine (session metadata, encrypted);
//   - facts the user confirmed ("remember ..."), kept as end-to-end
//     encrypted secrets named memory.<id>;
//   - the agents' own memory notes (memory-kind sessions).
// IndexMemory copies them into the local index that `hearth recall`, the
// prompt hooks and the MCP recall tool search.

const factPrefix = "memory."

// Fact is one remembered fact or habit.
type Fact struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Scope   string `json:"scope,omitempty"` // "" = everywhere, else a directory (e.g. a repository)
	Created int64  `json:"created"`
	Machine string `json:"machine,omitempty"`
	Source  string `json:"source,omitempty"` // user | agent name
}

// MemoryOn reports [memory] enabled (default true).
func MemoryOn(home string) bool {
	c, err := UserConfig(home)
	if err != nil || c.Memory == nil || c.Memory.Enabled == nil {
		return true
	}
	return *c.Memory.Enabled
}

// Remember stores a fact (end-to-end encrypted) and indexes it here at once.
func (e *Engine) Remember(text, scope, source string) (Fact, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Fact{}, errors.New("nothing to remember")
	}
	if len(text) > 2000 {
		return Fact{}, errors.New("too long for a fact (2000 bytes); keep it to one habit or decision")
	}
	if library.SecretInText(text) {
		return Fact{}, errors.New("this looks like it contains a credential; keep credentials in hearth secret, not in memory")
	}
	b := make([]byte, 6)
	rand.Read(b)
	host, _ := os.Hostname()
	f := Fact{ID: hex.EncodeToString(b), Text: text, Scope: scope, Created: time.Now().Unix(), Machine: host, Source: source}
	v, _ := json.Marshal(f)
	if err := e.SetSecret(Secret{Name: factPrefix + f.ID, Value: v}); err != nil {
		return f, err
	}
	if x, err := memory.Open(memory.Path(e.Home)); err == nil {
		d := factDoc(f)
		x.Apply([]memory.Doc{d}, map[string]string{d.ID: docHash(d)}, nil)
		x.Close()
	}
	return f, nil
}

// ForgetFact deletes a fact (id "f:<id>" or "<id>").
func (e *Engine) ForgetFact(id string) error {
	id = strings.TrimPrefix(id, "f:")
	if err := e.DeleteSecret(factPrefix + id); err != nil {
		return err
	}
	if x, err := memory.Open(memory.Path(e.Home)); err == nil {
		x.Apply(nil, nil, []string{"f:" + id})
		x.Close()
	}
	return nil
}

// Facts lists the remembered facts, newest first.
func (e *Engine) Facts() ([]Fact, error) {
	all, err := e.Secrets()
	if err != nil {
		return nil, err
	}
	var out []Fact
	for _, s := range all {
		if !strings.HasPrefix(s.Name, factPrefix) {
			continue
		}
		var f Fact
		if json.Unmarshal(s.Value, &f) == nil && f.Text != "" {
			out = append(out, f)
		}
	}
	sortFacts(out)
	return out, nil
}

func sortFacts(fs []Fact) {
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && fs[j].Created > fs[j-1].Created; j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}

func factDoc(f Fact) memory.Doc {
	title := f.Text
	if r := []rune(title); len(r) > 80 {
		title = string(r[:80]) + "…"
	}
	return memory.Doc{ID: "f:" + f.ID, Kind: "fact", Agent: f.Source, Title: title, Body: f.Text, Machine: f.Machine, Cwd: f.Scope, At: f.Created}
}

func docHash(d memory.Doc) string {
	h := sha256.Sum256([]byte(strings.Join([]string{d.Kind, d.Title, d.Body, d.Machine, d.Cwd, strconv.FormatInt(d.At, 10), d.Ref}, "\x00")))
	return hex.EncodeToString(h[:12])
}

// conversationDoc is the searchable form of a conversation's card.
func conversationDoc(rs RemoteSession) (memory.Doc, bool) {
	m := rs.Meta
	if rs.MetaErr != nil || !IsConversation(rs) || rs.Status == "expired" {
		return memory.Doc{}, false
	}
	var b strings.Builder
	if c := m.Card; c != nil {
		goal := c.Goal
		if strings.HasPrefix(goal, strings.TrimSuffix(m.Title, "…")) {
			goal = "" // the title already says it
		}
		for _, part := range []struct{ label, v string }{{"Asked", goal}, {"Outcome", c.Outcome}, {"Later asked", c.Asks}, {"Errors", c.Errors}, {"Commands", c.Cmds}, {"Files", c.Files}} {
			if part.v != "" {
				fmt.Fprintf(&b, "%s: %s\n", part.label, strings.ReplaceAll(part.v, "\n", " · "))
			}
		}
	}
	title := m.Title
	if title == "" {
		title = m.RelPath
	}
	at := m.LastActive
	if at == 0 {
		at = rs.ActiveAt
	}
	machine := m.Writer
	if machine == "" {
		machine = m.Host
	}
	return memory.Doc{ID: "s:" + rs.ID, Kind: "conversation", Agent: m.Agent, Title: title, Body: strings.TrimSpace(b.String()),
		Machine: machine, Cwd: m.Cwd, At: at, Ref: m.RelPath}, true
}

// MemoryStats is what IndexMemory did.
type MemoryStats struct {
	Conversations, Facts, Notes int
	Updated, Removed            int
	Took                        time.Duration
}

// IndexMemory brings the local index up to date with the server. Only
// changed entries are written; notes are downloaded only when they changed.
func (e *Engine) IndexMemory() (MemoryStats, error) {
	start := time.Now()
	var st MemoryStats
	if !MemoryOn(e.Home) {
		return st, nil
	}
	all, err := e.List()
	if err != nil {
		return st, err
	}
	facts, err := e.Facts()
	if err != nil {
		return st, err
	}
	x, err := memory.Open(memory.Path(e.Home))
	if err != nil {
		return st, err
	}
	defer x.Close()
	have, err := x.Hashes()
	if err != nil {
		return st, err
	}
	want := map[string]bool{}
	var put []memory.Doc
	hashes := map[string]string{}
	add := func(d memory.Doc, h string) {
		want[d.ID] = true
		if have[d.ID] != h {
			put = append(put, d)
			hashes[d.ID] = h
		}
	}
	for _, rs := range all {
		if d, ok := conversationDoc(rs); ok {
			st.Conversations++
			add(d, docHash(d))
			continue
		}
		if rs.MetaErr == nil && rs.Meta.Kind == adapter.KindMemory && rs.Status != "expired" && rs.Size > 0 && rs.Size <= 256<<10 {
			st.Notes++
			id, h := "n:"+rs.ID, fmt.Sprintf("g%d-%d", rs.Generation, rs.Size)
			want[id] = true
			if have[id] == h {
				continue
			}
			data, err := e.SessionData(rs)
			if err != nil {
				continue // next pass
			}
			body := string(data)
			if r := []rune(body); len(r) > 8000 {
				body = string(r[:8000])
			}
			put = append(put, memory.Doc{ID: id, Kind: "note", Agent: rs.Meta.Agent, Title: rs.Meta.Agent + " memory: " + rs.Meta.RelPath,
				Body: body, Machine: rs.Meta.Host, Cwd: rs.Meta.Cwd, At: rs.ActiveAt})
			hashes[id] = h
		}
	}
	for _, f := range facts {
		st.Facts++
		d := factDoc(f)
		add(d, docHash(d))
	}
	var del []string
	for id := range have {
		if !want[id] {
			del = append(del, id)
		}
	}
	if len(put) > 0 || len(del) > 0 {
		if err := x.Apply(put, hashes, del); err != nil {
			return st, err
		}
	}
	x.SetMeta("indexed_at", strconv.FormatInt(time.Now().Unix(), 10))
	st.Updated, st.Removed, st.Took = len(put), len(del), time.Since(start)
	return st, nil
}
