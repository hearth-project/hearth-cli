package client

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A machine that reaches the server only through a proxy: the site's
// proxies are tried when the server does not answer directly, and the one
// that works is used and remembered.
func TestAutoProxy(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") }))
	defer srv.Close()
	target := strings.TrimPrefix(srv.URL, "https://")
	// A CONNECT proxy that sends every tunnel to the test server.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "connect only", 405)
			return
		}
		up, err := net.Dial("tcp", target)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.WriteHeader(200)
		c, buf, _ := w.(http.Hijacker).Hijack()
		go func() { io.Copy(up, buf); up.Close() }()
		io.Copy(c, up)
		c.Close()
	}))
	defer proxy.Close()

	home := t.TempDir()
	for _, k := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY", "no_proxy", "NO_PROXY", "HEARTH_NO_AUTO_PROXY"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HEARTH_SERVER", "https://hearth.invalid:443")
	old := probeTimeout
	probeTimeout = 2 * time.Second
	defer func() { probeTimeout = old; AutoProxy = "" }()
	os.MkdirAll(SiteDir(home), 0o755)
	os.WriteFile(filepath.Join(SiteDir(home), "net.json"), []byte(`{"proxies": ["http://127.0.0.1:1", "`+proxy.URL+`"]}`), 0o644)

	autoProxyFor(home)
	if AutoProxy != proxy.URL || os.Getenv("https_proxy") != proxy.URL || !strings.Contains(os.Getenv("no_proxy"), "127.0.0.1") {
		t.Fatalf("picked %q, https_proxy %q", AutoProxy, os.Getenv("https_proxy"))
	}
	var st netState
	if loadJSON(netStatePath(home), &st) != nil || st.HTTPSProxy != proxy.URL {
		t.Fatalf("not remembered: %+v", st)
	}
	// Reachable directly: nothing changes.
	os.Unsetenv("https_proxy")
	os.Unsetenv("http_proxy")
	AutoProxy = ""
	t.Setenv("HEARTH_SERVER", srv.URL)
	home2 := t.TempDir()
	os.MkdirAll(SiteDir(home2), 0o755)
	os.WriteFile(filepath.Join(SiteDir(home2), "net.json"), []byte(`{"proxies": ["`+proxy.URL+`"]}`), 0o644)
	autoProxyFor(home2)
	if AutoProxy != "" || os.Getenv("https_proxy") != "" {
		t.Fatalf("direct route but proxy %q", AutoProxy)
	}
}
