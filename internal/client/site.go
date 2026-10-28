package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Site defaults are organisation-specific recipes, files and learn settings
// the server admin publishes (server --site). They are kept in <home>/site
// and used below the user's own library: recipes and files of the same
// name in the user's library win.

// SiteDir is the local copy of the server's site defaults.
func SiteDir(home string) string { return filepath.Join(home, "site") }

const headerSiteSHA256 = "X-Hearth-Site-SHA256"

type siteState struct {
	SHA256 string `json:"sha256"`
}

func siteStatePath(home string) string { return filepath.Join(home, "site-state.json") }

// SiteResult describes one site sync.
type SiteResult struct {
	Op     string // up-to-date, pulled, none, removed
	SHA256 string
}

// SiteSync fetches the server's site defaults if they changed.
func (e *Engine) SiteSync() (SiteResult, error) { return SiteSyncWith(e.Home, e.API) }

// SiteSyncFrom fetches the site defaults of a server without logging in
// (the installer does this before `hearth setup`, so organisation defaults
// such as proxies apply from the first step).
func SiteSyncFrom(home, server string) (SiteResult, error) {
	hc, err := HTTPClient(home)
	if err != nil {
		return SiteResult{}, err
	}
	r, err := SiteSyncWith(home, NewAPI(server, "", hc))
	if err == nil {
		_ = RememberServer(home, server) // `hearth setup` then needs no --server
	}
	return r, err
}

func serverHintPath(home string) string { return filepath.Join(home, "server-url") }

// RememberServer records the server the installer came from.
func RememberServer(home, server string) error {
	if server == "" {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(serverHintPath(home), []byte(strings.TrimRight(server, "/")+"\n"), 0o600)
}

// DefaultServer is the server to use when none is given: this home's
// login, $HEARTH_SERVER, or the one the installer was downloaded from
// (also from the default home, for a workspace made after installing).
func DefaultServer(home string) string {
	if cfg, err := LoadConfig(home); err == nil && cfg.Server != "" {
		return cfg.Server
	}
	if s := os.Getenv("HEARTH_SERVER"); s != "" {
		return s
	}
	for _, h := range []string{home, DefaultHome()} {
		if b, err := os.ReadFile(serverHintPath(h)); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
	}
	return ""
}

// SiteSyncWith fetches the site defaults through api (token optional).
func SiteSyncWith(home string, a *API) (SiteResult, error) {
	var st siteState
	_ = loadJSON(siteStatePath(home), &st)
	if _, err := os.Stat(SiteDir(home)); err != nil {
		st.SHA256 = "" // local copy gone: fetch again
	}
	var data []byte
	hdr, err := a.do("GET", "/v1/site", nil, nil, map[string]string{headerSiteSHA256: st.SHA256}, &data)
	switch StatusOf(err) {
	case 304:
		return SiteResult{Op: "up-to-date", SHA256: st.SHA256}, nil
	case 404:
		if _, serr := os.Stat(SiteDir(home)); serr == nil {
			if err := os.RemoveAll(SiteDir(home)); err != nil {
				return SiteResult{}, err
			}
			os.Remove(siteStatePath(home))
			return SiteResult{Op: "removed"}, nil
		}
		return SiteResult{Op: "none"}, nil
	}
	if err != nil {
		return SiteResult{}, err
	}
	sum := ""
	if hdr != nil {
		sum = hdr.Get(headerSiteSHA256)
	}
	tmp := SiteDir(home) + ".incoming-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	got, err := library.Extract(data, tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return SiteResult{}, fmt.Errorf("extract site defaults: %w", err)
	}
	if sum != "" && got != sum {
		os.RemoveAll(tmp)
		return SiteResult{}, fmt.Errorf("site bundle hash mismatch")
	}
	os.RemoveAll(SiteDir(home))
	if err := os.Rename(tmp, SiteDir(home)); err != nil {
		return SiteResult{}, err
	}
	return SiteResult{Op: "pulled", SHA256: got}, saveJSON(siteStatePath(home), siteState{SHA256: got})
}

// siteChoices is library/site.json: the site recipes this user opted out of
// (synced with the library, so the choice holds on every machine).
type siteChoices struct {
	Disabled []string `json:"disabled,omitempty"`
}

func siteChoicesPath(home string) string { return filepath.Join(LibraryDir(home), "site.json") }

// SiteDisabled lists the site recipes the user turned off.
func SiteDisabled(home string) []string {
	var c siteChoices
	_ = loadJSON(siteChoicesPath(home), &c)
	return c.Disabled
}

// SetSiteRecipe turns a site recipe off (or back on) for this user.
func SetSiteRecipe(home, name string, on bool) error {
	var c siteChoices
	_ = loadJSON(siteChoicesPath(home), &c)
	var out []string
	for _, n := range c.Disabled {
		if n != name {
			out = append(out, n)
		}
	}
	if !on {
		out = append(out, name)
	}
	c.Disabled = out
	if err := os.MkdirAll(LibraryDir(home), 0o755); err != nil {
		return err
	}
	return saveJSON(siteChoicesPath(home), c)
}
