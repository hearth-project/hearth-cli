package client

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PortalDocs are the per-user documents the web console shows, fetched for
// the macOS app's own pages: key -> path under /v1/me.
var PortalDocs = map[string]string{
	"me":       "/v1/me",
	"library":  "/v1/me/library",
	"teams":    "/v1/me/teams",
	"settings": "/v1/me/settings",
	"devices":  "/v1/me/devices",
	"intents":  "/v1/me/intents",
}

// Portal returns the console's documents (see PortalDocs) as JSON, read
// through one short console session opened with this device's login. A
// document that fails to load is left out and named in errs, so one broken
// page does not blank the others.
func (e *Engine) Portal() (docs map[string]json.RawMessage, errs map[string]string, err error) {
	return e.fetchDocs(PortalDocs)
}

// AdminDocs are the administration pages' documents (/v1/admin/...), key ->
// path. Only an admin account can read them.
var AdminDocs = map[string]string{
	"overview": "/v1/admin/overview",
	"users":    "/v1/admin/users",
	"invites":  "/v1/admin/invites",
	"teams":    "/v1/admin/teams",
	"settings": "/v1/admin/settings",
	"audit":    "/v1/admin/audit?limit=300",
}

// AdminPortal is Portal for the administration pages (see AdminDocs).
func (e *Engine) AdminPortal() (map[string]json.RawMessage, map[string]string, error) {
	return e.fetchDocs(AdminDocs)
}

// AdminCall runs one request against /v1/admin/... in a short console
// session and returns the JSON reply (nil for an empty one). body may be nil.
func (e *Engine) AdminCall(method, path string, body json.RawMessage) (json.RawMessage, error) {
	if !strings.HasPrefix(path, "/v1/admin/") {
		return nil, fmt.Errorf("only /v1/admin/ paths are allowed: %s", path)
	}
	return e.ConsoleCall(method, path, body)
}

// ConsoleCall runs one console request (/v1/me... or /v1/admin/...) in a
// short console session and returns the JSON reply (nil for an empty one).
func (e *Engine) ConsoleCall(method, path string, body json.RawMessage) (json.RawMessage, error) {
	if path != "/v1/me" && !strings.HasPrefix(path, "/v1/me/") && !strings.HasPrefix(path, "/v1/admin/") {
		return nil, fmt.Errorf("only /v1/me and /v1/admin/ paths are allowed: %s", path)
	}
	web, hdr, err := e.consoleAPI()
	if err != nil {
		return nil, err
	}
	var in any
	if len(body) > 0 {
		in = body
	}
	var raw json.RawMessage
	if _, err := web.do(method, path, in, nil, hdr, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// consoleAPI opens a console session with this device's login and returns
// a client that sends its cookie.
func (e *Engine) consoleAPI() (*API, map[string]string, error) {
	path, err := e.API.AdminLink()
	if err != nil {
		return nil, nil, err
	}
	cookie, err := e.consoleSession(path)
	if err != nil {
		return nil, nil, err
	}
	return NewAPI(e.API.Base, "", e.API.HC), map[string]string{"Cookie": cookie, "X-Hearth-Admin": "1"}, nil
}

func (e *Engine) fetchDocs(paths map[string]string) (docs map[string]json.RawMessage, errs map[string]string, err error) {
	web, hdr, err := e.consoleAPI()
	if err != nil {
		return nil, nil, err
	}
	docs, errs = map[string]json.RawMessage{}, map[string]string{}
	for k, p := range paths {
		var raw json.RawMessage
		if _, err := web.do("GET", p, nil, nil, hdr, &raw); err != nil {
			errs[k] = fmt.Sprint(err)
			continue
		}
		docs[k] = raw
	}
	if len(docs) == 0 {
		for _, m := range errs {
			return nil, errs, fmt.Errorf("%s", m)
		}
	}
	return docs, errs, nil
}
