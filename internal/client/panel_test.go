package client

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// The admin panel: sign-in link -> cookie session, CSRF header, approval
// registration mode, audit log.
func TestAdminPanel(t *testing.T) {
	en := newEnv(t)
	alice := en.machine("alice") // dev@example.com
	bob := en.user("bob@example.com", "bob")

	if err := en.srv.SetAdmin("dev@example.com", true); err != nil {
		t.Fatal(err)
	}
	if err := en.srv.SetAdmin("dev@example.com", false); err == nil {
		t.Fatal("removed the last admin")
	}
	path, err := alice.API.AdminLink()
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Get(en.url + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "hearth_session" {
			cookie = c
		}
	}
	if resp.StatusCode != http.StatusSeeOther || cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("login: %d %+v", resp.StatusCode, cookie)
	}
	if r, _ := hc.Get(en.url + "/admin/login?t=" + strings.Repeat("x", 20)); r.StatusCode != 401 {
		t.Fatalf("forged link: %d", r.StatusCode)
	}
	call := func(method, p string, body any, csrf bool) (int, []byte) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, en.url+"/v1/admin"+p, rd)
		req.AddCookie(cookie)
		if csrf {
			req.Header.Set("X-Hearth-Admin", "1")
		}
		r, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, b
	}
	if code, b := call("GET", "/overview", nil, false); code != 200 || !bytes.Contains(b, []byte(`"series"`)) {
		t.Fatalf("overview %d %s", code, b)
	}
	req, _ := http.NewRequest("GET", en.url+"/v1/admin/users", nil)
	if r, _ := hc.Do(req); r.StatusCode != 401 {
		t.Fatalf("no cookie: %d", r.StatusCode)
	}
	if code, _ := call("PUT", "/settings", map[string]any{"registration": "approval", "default_quota": 0, "client_auto_update": true}, false); code != 403 {
		t.Fatalf("mutation without CSRF header: %d", code)
	}
	if code, b := call("PUT", "/settings", map[string]any{"registration": "approval", "default_quota": 0, "client_auto_update": true}, true); code != 204 {
		t.Fatalf("settings %d %s", code, b)
	}
	// Approval mode: carol signs up without an invite and waits.
	if _, err := Register(t.TempDir(), en.url, "carol@example.com", []byte(pw), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Login(t.TempDir(), en.url, "carol@example.com", []byte(pw), "c"); StatusOf(err) != 403 || !strings.Contains(err.Error(), "approval") {
		t.Fatalf("pending login: %v", err)
	}
	if code, b := call("GET", "/users", nil, false); code != 200 || !bytes.Contains(b, []byte(`"pending"`)) {
		t.Fatalf("users %d %s", code, b)
	}
	if code, b := call("POST", "/users/carol@example.com/status", map[string]string{"status": "active"}, true); code != 204 {
		t.Fatalf("approve %d %s", code, b)
	}
	if _, err := Login(t.TempDir(), en.url, "carol@example.com", []byte(pw), "c"); err != nil {
		t.Fatalf("approved login: %v", err)
	}
	if code, _ := call("POST", "/users/dev@example.com/status", map[string]string{"status": "disabled"}, true); code != 400 {
		t.Fatalf("disabled myself: %d", code)
	}
	if code, b := call("POST", "/invites", map[string]int{"days": 3}, true); code != 201 || !bytes.Contains(b, []byte(`"inv-`)) {
		t.Fatalf("invite %d %s", code, b)
	}
	if code, b := call("GET", "/audit", nil, false); code != 200 || !bytes.Contains(b, []byte("approve-user")) || !bytes.Contains(b, []byte("panel-login")) {
		t.Fatalf("audit %d %s", code, b)
	}
	// Teams: create one for bob, add and remove members, change roles.
	if code, b := call("POST", "/teams", map[string]string{"name": "infer", "owner": "bob@example.com"}, true); code != 201 {
		t.Fatalf("create team %d %s", code, b)
	}
	if code, _ := call("POST", "/teams", map[string]string{"name": "infer"}, true); code != 409 {
		t.Fatalf("duplicate team: %d", code)
	}
	if code, b := call("PUT", "/teams/infer/members/carol@example.com", map[string]string{"role": "maintainer"}, true); code != 204 {
		t.Fatalf("add member %d %s", code, b)
	}
	if code, _ := call("PUT", "/teams/infer/members/nobody@example.com", map[string]string{"role": "member"}, true); code != 400 {
		t.Fatalf("unknown user added: %d", code)
	}
	if code, b := call("GET", "/teams/infer", nil, false); code != 200 || !bytes.Contains(b, []byte(`"carol@example.com","role":"maintainer"`)) {
		t.Fatalf("team detail %d %s", code, b)
	}
	if code, _ := call("DELETE", "/teams/infer/members/bob@example.com", nil, true); code != 400 {
		t.Fatalf("removed the last owner: %d", code)
	}
	if code, _ := call("DELETE", "/teams/infer/members/carol@example.com", nil, true); code != 204 {
		t.Fatalf("remove member: %d", code)
	}
	if ts, err := bob.API.Teams(); err != nil || len(ts) != 1 || ts[0].Role != "owner" {
		t.Fatalf("bob's teams %+v %v", ts, err)
	}
	// Online clients per user.
	if err := bob.API.Heartbeat(); err != nil {
		t.Fatal(err)
	}
	if code, b := call("GET", "/users", nil, false); code != 200 || !bytes.Contains(b, []byte(`"online":[{"name":"bob"`)) {
		t.Fatalf("online clients %d %s", code, b)
	}
	// Account sign-in: the login key derived from the master password.
	pwLogin := func(email, password string) int {
		pre, err := alice.API.Prelogin(email)
		if err != nil {
			t.Fatal(err)
		}
		uk, err := crypto.DeriveUserKeys([]byte(password), pre.Salt, pre.KDF)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(map[string]any{"email": email, "auth_key": uk.AuthKey})
		r, err := hc.Post(en.url+"/v1/admin/login", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode == 204 {
			found := false
			for _, c := range r.Cookies() {
				found = found || (c.Name == "hearth_session" && c.HttpOnly)
			}
			if !found {
				t.Fatal("no session cookie")
			}
		}
		return r.StatusCode
	}
	if code := pwLogin("dev@example.com", pw); code != 204 {
		t.Fatalf("admin password login: %d", code)
	}
	if code := pwLogin("dev@example.com", "wrong password"); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	// Every user signs in to the console; admin pages stay closed to them.
	if code := pwLogin("bob@example.com", pw); code != 204 {
		t.Fatalf("user login: %d", code)
	}
	libWrite(t, bob, "skills/bench/SKILL.md", "---\nname: bench\ndescription: run benchmarks\n---\nsteps\n")
	libWrite(t, bob, "agents/rev.md", "---\nname: rev\ndescription: reviews code\n---\nReview.\n")
	libWrite(t, bob, "skills/aaa/SKILL.md", "---\nname: aaa\ndescription: unused\n---\nx\n")
	if _, err := bob.LibPush(false); err != nil {
		t.Fatal(err)
	}
	day := en.clock.now().UTC().Format("2006-01-02")
	if _, err := bob.API.PutUserUsage([]api.UsageCount{{Type: "skill", Name: "bench", Day: day, Count: 3}}); err != nil {
		t.Fatal(err)
	}
	bobCookie := sessionCookie(t, hc, en.url, bob)
	{
		req, _ := http.NewRequest("GET", en.url+"/v1/me/library", nil)
		req.AddCookie(bobCookie)
		r, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if !bytes.Contains(b, []byte(`"type":"skill","name":"bench","description":"run benchmarks","resident":true`)) || !bytes.Contains(b, []byte(`"type":"agent","name":"rev","description":"reviews code"`)) {
			t.Fatalf("library summary %s", b)
		}
		if i, j := bytes.Index(b, []byte(`"name":"bench"`)), bytes.Index(b, []byte(`"name":"aaa"`)); i < 0 || j < 0 || i > j || !bytes.Contains(b, []byte(`"uses":3`)) {
			t.Fatalf("not sorted by use: %s", b)
		}
	}
	// One-click sharing from the console (bob owns team infer).
	share := func(body map[string]any) (int, []byte) {
		bb, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", en.url+"/v1/me/share", bytes.NewReader(bb))
		req.AddCookie(bobCookie)
		req.Header.Set("X-Hearth-Admin", "1")
		r, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		out, _ := io.ReadAll(r.Body)
		return r.StatusCode, out
	}
	if code, b := share(map[string]any{"type": "skill", "name": "bench", "teams": []string{"infer", "nope"}}); code != 200 ||
		!bytes.Contains(b, []byte(`{"team":"infer","version":1,"created":true}`)) || !bytes.Contains(b, []byte(`"team":"nope","created":false,"error":"no such team"`)) {
		t.Fatalf("share %d %s", code, b)
	}
	if code, b := share(map[string]any{"type": "skill", "name": "bench", "teams": []string{"infer"}}); code != 200 || !bytes.Contains(b, []byte(`"version":1,"created":false`)) {
		t.Fatalf("share again %d %s", code, b)
	}
	if code, b := share(map[string]any{"type": "agent", "name": "rev", "teams": []string{"infer"}}); code != 200 || !bytes.Contains(b, []byte(`"created":true`)) {
		t.Fatalf("share agent %d %s", code, b)
	}
	if code, _ := share(map[string]any{"type": "skill", "name": "missing", "teams": []string{"infer"}}); code != 404 {
		t.Fatalf("share missing: %d", code)
	}
	// Team pages rank shared items by the members' use.
	{
		req, _ := http.NewRequest("GET", en.url+"/v1/me/teams", nil)
		req.AddCookie(bobCookie)
		r, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if i, j := bytes.Index(b, []byte(`"name":"bench"`)), bytes.Index(b, []byte(`"name":"rev"`)); i < 0 || j < 0 || i > j || !bytes.Contains(b, []byte(`"uses":3,"users":1`)) {
			t.Fatalf("team heat %s", b)
		}
	}
	// Console changes: queued in the browser, applied by the machine.
	post := func(path string, body any) (int, []byte) {
		bb, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", en.url+path, bytes.NewReader(bb))
		req.AddCookie(bobCookie)
		req.Header.Set("X-Hearth-Admin", "1")
		r, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		out, _ := io.ReadAll(r.Body)
		return r.StatusCode, out
	}
	if code, b := post("/v1/me/intents", map[string]any{"kind": "resident", "data": map[string]any{"names": []string{"bench"}}}); code != 201 {
		t.Fatalf("intent %d %s", code, b)
	}
	if code, _ := post("/v1/me/intents", map[string]any{"kind": "rm -rf", "data": map[string]any{}}); code != 400 {
		t.Fatalf("unknown intent accepted: %d", code)
	}
	if n, err := bob.API.HeartbeatIntents(); err != nil || n != 1 {
		t.Fatalf("heartbeat intents %d %v", n, err)
	}
	if lines, err := bob.ApplyIntents(); err != nil || len(lines) == 0 {
		t.Fatalf("apply %v %v", lines, err)
	}
	if cfg, _ := UserConfig(bob.Home); cfg.Skills == nil || len(cfg.Skills.Resident) != 1 || cfg.Skills.Resident[0] != "bench" {
		t.Fatalf("resident not applied: %+v", cfg.Skills)
	}
	if n, _ := bob.API.HeartbeatIntents(); n != 0 {
		t.Fatalf("intent left: %d", n)
	}
	// Same package as the client would publish: a client re-publish is a no-op.
	if v, created, err := bob.Publish("infer", "skill", "bench", false); err != nil || created || v != 1 {
		t.Fatalf("client publish after console share: v%d created=%v %v", v, created, err)
	}

	for p, want := range map[string]int{"/v1/me": 200, "/v1/me/devices": 200, "/v1/me/library": 200, "/v1/me/teams": 200, "/v1/admin/users": 401} {
		req, _ := http.NewRequest("GET", en.url+p, nil)
		req.AddCookie(bobCookie)
		r, err := hc.Do(req)
		if err != nil || r.StatusCode != want {
			t.Fatalf("bob GET %s: %v %v, want %d", p, r.StatusCode, err, want)
		}
		r.Body.Close()
	}
	// The static page is served with a strict CSP.
	r, err := hc.Get(en.url + "/admin")
	if err != nil || r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("panel page: %v %v", r, err)
	}
	if r, _ := hc.Get(en.url + "/admin/panel.js"); r.StatusCode != 200 {
		t.Fatalf("panel.js %d", r.StatusCode)
	}
	if r, _ := hc.Get(en.url + "/admin/../schema.sql"); r.StatusCode == 200 {
		t.Fatal("served a file outside the panel")
	}
}

// sessionCookie signs m in to the console with a link and returns the cookie.
func sessionCookie(t *testing.T, hc *http.Client, url string, m *machine) *http.Cookie {
	t.Helper()
	path, err := m.API.AdminLink()
	if err != nil {
		t.Fatal(err)
	}
	r, err := hc.Get(url + path)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	for _, c := range r.Cookies() {
		if c.Name == "hearth_session" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}
