package server

import (
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Site defaults: organisation-specific setup an admin keeps on the server
// instead of in the open-source code (internal git hosts, proxies, model
// gateway recipes, company login-token locations). The directory
// (--site, default <data>/site) has the library layout:
//
//	recipes/*.toml   restore recipes offered to every user
//	files/...        files those recipes install
//	learn.json       {"token_files": [...]} extra login-token globs for `hearth learn`
//
// Logged-in clients fetch it as one bundle (GET /v1/site) and use it below
// their own library: a user's recipe or file with the same name wins.

const headerSiteSHA256 = "X-Hearth-Site-SHA256"

type siteCache struct {
	mu    sync.Mutex
	at    time.Time
	data  []byte
	sum   string
	empty bool
}

// bundle re-reads the directory at most every 10 s (admins edit it live).
func (c *siteCache) bundle(dir string, now time.Time) ([]byte, string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && now.Sub(c.at) < 10*time.Second && now.Sub(c.at) >= 0 {
		return c.data, c.sum, c.empty, nil
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		c.at, c.data, c.sum, c.empty = now, nil, "", true
		return nil, "", true, nil
	}
	data, sum, err := library.Library{Root: dir}.Bundle()
	if err != nil {
		return nil, "", false, err
	}
	c.at, c.data, c.sum, c.empty = now, data, sum, false
	return data, sum, false, nil
}

func (s *Server) handleSite(w http.ResponseWriter, r *http.Request) {
	data, sum, empty, err := s.site.bundle(s.cfg.SiteDir, s.cfg.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "site bundle: "+err.Error())
		return
	}
	if empty {
		writeErr(w, http.StatusNotFound, "no site defaults")
		return
	}
	w.Header().Set(headerSiteSHA256, sum)
	if r.Header.Get(headerSiteSHA256) == sum {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Write(data)
}
