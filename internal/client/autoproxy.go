package client

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Machines that reach the server only through a proxy: when this process
// has none set and the server does not answer directly, the proxies the
// organisation lists in the site defaults (net.json) are tried in order,
// and the first one that reaches the server is used and remembered.

type siteNet struct {
	Proxies []string `json:"proxies,omitempty"`
}

// SiteProxies are the organisation's proxies (site defaults net.json).
func SiteProxies(home string) []string {
	var n siteNet
	loadJSON(filepath.Join(siteDirOrDefault(home), "net.json"), &n)
	return n.Proxies
}

// AutoProxy is the proxy this process picked by itself ("" = none).
var AutoProxy string

var autoProxyOnce sync.Map // server host -> struct{}

// probeTimeout bounds each reachability test.
var probeTimeout = 4 * time.Second

func autoProxyFor(home string) {
	if p := proxyFromEnv(); p.HTTPSProxy != "" || p.HTTPProxy != "" || os.Getenv("HEARTH_NO_AUTO_PROXY") != "" {
		return
	}
	server := DefaultServer(home)
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return
	}
	if _, done := autoProxyOnce.LoadOrStore(u.Host, struct{}{}); done {
		return
	}
	proxies := SiteProxies(home)
	if len(proxies) == 0 || isLoopbackURL(server) {
		return
	}
	if reachVia(server, "") {
		return
	}
	for _, p := range proxies {
		if reachVia(server, p) {
			os.Setenv("https_proxy", p)
			os.Setenv("http_proxy", p)
			if np := os.Getenv("no_proxy"); !strings.Contains(np, "127.0.0.1") {
				os.Setenv("no_proxy", strings.TrimPrefix(np+",127.0.0.1,localhost,::1", ","))
			}
			AutoProxy = p
			RememberProxy(home)
			return
		}
	}
}

// reachVia asks the server's /healthz directly (proxy "") or through a
// proxy. Only reachability: the certificate is checked by the real requests.
func reachVia(server, proxy string) bool {
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: probeTimeout}).DialContext,
		TLSHandshakeTimeout: probeTimeout,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // reachability probe only
		Proxy:               func(*http.Request) (*url.URL, error) { return nil, nil },
	}
	if proxy != "" {
		pu, err := url.Parse(proxy)
		if err != nil {
			return false
		}
		tr.Proxy = http.ProxyURL(pu)
	}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr, Timeout: 2 * probeTimeout}
	resp, err := c.Get(strings.TrimRight(server, "/") + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}
