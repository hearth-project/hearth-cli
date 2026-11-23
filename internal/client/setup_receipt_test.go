package client

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A landing page (or an older server) is not confirmation that the existing
// device consumed the install command. Never forward either proof on redirect.
func TestReuseInstallTokenReceipt(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed reuse redirect") }))
	defer target.Close()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"confirmed", 200, `{"device_id":"mine"}`, true}, {"wrong device", 200, `{"device_id":"other"}`, false},
		{"empty", 204, "", false}, {"landing", 200, "<html></html>", false}, {"missing", 404, "", false}, {"old method", 405, "", false}, {"redirect", 307, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 307 {
					w.Header().Set("Location", target.URL)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			t.Setenv("HOME", t.TempDir())
			cfg := &Config{Server: ts.URL, DeviceID: "mine", Token: "test-bearer", Email: "dev@example.org"}
			err := ReuseInstallToken(DefaultHome(), cfg, "0123456789abcdef."+base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
			if (err == nil) != tc.want {
				t.Fatalf("reuse receipt: %v", err)
			}
			if (tc.status == 404 || tc.status == 405) && !strings.Contains(err.Error(), "upgrade") {
				t.Fatal("missing server upgrade guidance")
			}
		})
	}
}
