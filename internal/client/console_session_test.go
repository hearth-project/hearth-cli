package client

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The macOS app and `hearth app call` reach /v1/me and /v1/admin through a
// short session: a one-time link from the device's login, redeemed for a
// cookie. There is no web page behind it any more.
func TestConsoleSessionWithoutWebPages(t *testing.T) {
	en := newEnv(t)
	if err := en.srv.SetAdmin("dev@example.com", true); err != nil {
		t.Fatal(err)
	}
	m := en.machine("a")

	raw, err := m.ConsoleCall("GET", "/v1/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	var me struct {
		Email string `json:"email"`
		Admin bool   `json:"admin"`
	}
	if err := json.Unmarshal(raw, &me); err != nil || me.Email != "dev@example.com" || !me.Admin {
		t.Fatalf("/v1/me: %s %v", raw, err)
	}
	if _, err := m.AdminCall("GET", "/v1/admin/overview", nil); err != nil {
		t.Fatalf("admin overview: %v", err)
	}
	if _, err := m.AdminCall("GET", "/v1/me", nil); err == nil {
		t.Fatal("AdminCall must only reach /v1/admin")
	}

	// The link is for one session endpoint, and a bad token opens nothing.
	path, err := m.API.AdminLink()
	if err != nil || !strings.HasPrefix(path, "/v1/web/session?t=") {
		t.Fatalf("link path %q %v", path, err)
	}
	resp, err := http.Get(en.url + "/v1/web/session?t=nonsense")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || len(resp.Cookies()) != 0 {
		t.Fatalf("bad link: HTTP %d, %d cookies", resp.StatusCode, len(resp.Cookies()))
	}

	// The pages and what only they used are gone.
	for _, p := range []string{"/app", "/app/", "/app/panel.js", "/admin", "/admin/login", "/app/login?t=x"} {
		resp, err := http.Get(en.url + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: HTTP %d, want 404", p, resp.StatusCode)
		}
	}
	for _, p := range []string{"/v1/web/login", "/v1/admin/login", "/v1/web/logout", "/v1/me/vault-key"} {
		resp, err := http.Post(en.url+p, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: HTTP %d, want 404 or 405", p, resp.StatusCode)
		}
	}
	page, err := http.Get(en.url + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	buf := make([]byte, 1<<16)
	n, _ := page.Body.Read(buf)
	if strings.Contains(string(buf[:n]), `href="/app"`) {
		t.Error("the download page still links to /app")
	}
}
