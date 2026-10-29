package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Latest releases of the tools of the client setup guide (Codex CLI, Claude
// Code, codex-mixin), so the console and `hearth machine list` can say how
// far behind a machine is. The server looks them up itself every few hours
// (npm registry, GitHub release feed); clients only report what they have.
// Off unless Config.VersionCheck is set (servers without internet access:
// the machines then show missing tools and failed checks only).

type toolSource struct {
	tool string
	npm  string // npm package
	gh   string // GitHub owner/repo
}

var toolSources = []toolSource{
	{tool: "codex", npm: "@openai/codex"},
	{tool: "claude", npm: "@anthropic-ai/claude-code"},
	{tool: "codex-mixin", gh: "Edward-lyz/codex-mixin"},
}

const (
	versionRefresh = 6 * time.Hour
	versionRetry   = 30 * time.Minute
)

// toolVersions holds, per tool, its stable releases newest first.
type toolVersions struct {
	mu      sync.RWMutex
	byTool  map[string][]string
	checked time.Time
	stop    chan struct{}
}

func newToolVersions() *toolVersions {
	return &toolVersions{byTool: map[string][]string{}, stop: make(chan struct{})}
}

func (v *toolVersions) get(tool string) []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.byTool[tool]
}

func (v *toolVersions) set(tool string, list []string) {
	v.mu.Lock()
	v.byTool[tool] = list
	v.checked = time.Now()
	v.mu.Unlock()
}

// versionLoop refreshes the releases until the server closes.
func (s *Server) versionLoop() {
	for {
		wait := versionRefresh
		if err := s.refreshToolVersions(); err != nil {
			s.cfg.Logger.Printf("tool versions: %v", err)
			wait = versionRetry
		}
		select {
		case <-s.versions.stop:
			return
		case <-time.After(wait):
		}
	}
}

func (s *Server) refreshToolVersions() error {
	var errs []string
	for _, src := range toolSources {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		var list []string
		var err error
		if src.npm != "" {
			list, err = npmVersions(ctx, src.npm)
		} else {
			list, err = githubVersions(ctx, src.gh)
		}
		cancel()
		if err != nil || len(list) == 0 {
			errs = append(errs, fmt.Sprintf("%s: %v", src.tool, err))
			continue
		}
		s.versions.set(src.tool, list)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

var (
	stableRE  = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	ghTagRE   = regexp.MustCompile(`/releases/tag/v?(\d+\.\d+\.\d+)"`)
	semverAny = regexp.MustCompile(`\d+\.\d+\.\d+`)
)

// releaseClient waits longer for slow TLS through proxies than the default.
var releaseClient = func() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSHandshakeTimeout = 30 * time.Second
	return &http.Client{Transport: tr}
}()

func fetch(ctx context.Context, url, accept string) (b []byte, err error) {
	for try := 0; try < 2; try++ {
		if b, err = fetchOnce(ctx, url, accept); err == nil || ctx.Err() != nil {
			return b, err
		}
	}
	return b, err
}

func fetchOnce(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := releaseClient.Do(req) // honours HTTPS_PROXY
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

func npmVersions(ctx context.Context, pkg string) ([]string, error) {
	reg := strings.TrimRight(os.Getenv("HEARTH_NPM_REGISTRY"), "/")
	if reg == "" {
		reg = "https://registry.npmjs.org"
	}
	b, err := fetch(ctx, reg+"/"+pkg, "application/vnd.npm.install-v1+json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	var out []string
	for v := range doc.Versions {
		if stableRE.MatchString(v) {
			out = append(out, v)
		}
	}
	sortVersionsDesc(out)
	if len(out) > 200 {
		out = out[:200]
	}
	return out, nil
}

func githubVersions(ctx context.Context, repo string) ([]string, error) {
	b, err := fetch(ctx, "https://github.com/"+repo+"/releases.atom", "")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range ghTagRE.FindAllStringSubmatch(string(b), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sortVersionsDesc(out)
	return out, nil
}

// cmpVersion compares x.y.z versions (-1, 0, 1).
func cmpVersion(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func sortVersionsDesc(v []string) {
	sort.Slice(v, func(i, j int) bool { return cmpVersion(v[i], v[j]) > 0 })
}

// behind returns the newest release and how many releases are newer than
// have (more = at least that many: the list is the newest releases only).
func behind(releases []string, have string) (latest string, n int, more bool) {
	if len(releases) == 0 || have == "" {
		return "", 0, false
	}
	have = semverAny.FindString(have)
	for _, r := range releases {
		if cmpVersion(r, have) <= 0 {
			return releases[0], n, false
		}
		n++
	}
	return releases[0], n, true
}
