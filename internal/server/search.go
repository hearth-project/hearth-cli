package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/search"
)

// Team packages are plaintext, so the server keeps a ready BM25 index per
// team in memory: a search is a single in-memory lookup, with no package
// download on the client. The index is rebuilt lazily after a publish; other
// server instances notice the team's rev change within revCheckEvery.
type searchCache struct {
	mu    sync.Mutex
	teams map[int64]*teamIndex
}

type teamIndex struct {
	once    sync.Once
	ix      *search.Index
	meta    map[string]searchRow // "type:name"
	err     error
	rev     int64
	ready   atomic.Bool
	checked atomic.Int64 // unix nanos of the last rev check
}

const revCheckEvery = 2 * time.Second

func newSearchCache() *searchCache { return &searchCache{teams: map[int64]*teamIndex{}} }

func (c *searchCache) invalidate(teamID int64) {
	c.mu.Lock()
	delete(c.teams, teamID)
	c.mu.Unlock()
}

func (c *searchCache) get(st *store, teamID int64) (*teamIndex, error) {
	c.mu.Lock()
	t := c.teams[teamID]
	c.mu.Unlock()
	if t != nil && t.ready.Load() && time.Since(time.Unix(0, t.checked.Load())) > revCheckEvery {
		// One primary-key lookup every few seconds per team.
		if rev, err := st.teamRev(teamID); err == nil && rev != t.rev {
			c.invalidate(teamID)
		} else {
			t.checked.Store(time.Now().UnixNano())
		}
	}
	c.mu.Lock()
	t = c.teams[teamID]
	if t == nil {
		t = &teamIndex{}
		c.teams[teamID] = t
	}
	c.mu.Unlock()
	t.once.Do(func() {
		rev, err := st.teamRev(teamID) // before the rows: a later publish is noticed
		if err != nil {
			t.err = err
			return
		}
		t.rev = rev
		t.checked.Store(time.Now().UnixNano())
		rows, err := st.searchRows(teamID)
		if err != nil {
			t.err = err
			return
		}
		docs := make([]search.Skill, 0, len(rows))
		t.meta = map[string]searchRow{}
		for _, r := range rows {
			key := r.Type + ":" + r.Name
			docs = append(docs, search.NewSkill(key, r.Description, r.Text, r.Type))
			t.meta[key] = r
		}
		t.ix = search.Build(docs)
		t.ready.Store(true)
	})
	if t.err != nil {
		c.invalidate(teamID)
	}
	return t, t.err
}

// packageText extracts the start of SKILL.md / AGENT.md from a bundle for
// indexing (bounded: 64 KiB read, 4 KiB kept by the indexer).
func packageText(data []byte) string {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return ""
		}
		if hdr.Name == "SKILL.md" || hdr.Name == "AGENT.md" {
			b, _ := io.ReadAll(io.LimitReader(tr, 64<<10))
			_, body := search.SplitSkillMD(b)
			return body
		}
	}
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	t, ok := s.team(w, r, "reader")
	if !ok {
		return
	}
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	ti, err := s.search.get(s.st, t.ID)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	res := ti.ix.Search(q, limit)
	out := make([]api.SearchHit, 0, len(res))
	for _, h := range res {
		m := ti.meta[h.Name]
		out = append(out, api.SearchHit{Type: m.Type, Name: m.Name, Description: m.Description, Version: m.Version, Score: h.Score})
	}
	writeJSON(w, http.StatusOK, out)
}
