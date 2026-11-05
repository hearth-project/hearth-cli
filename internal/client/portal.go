package client

import (
	"encoding/json"
	"fmt"
)

// PortalDocs are the per-user documents the web console shows, fetched for
// the macOS app's own pages: key -> path under /v1/me.
var PortalDocs = map[string]string{
	"me":       "/v1/me",
	"library":  "/v1/me/library",
	"teams":    "/v1/me/teams",
	"settings": "/v1/me/settings",
}

// Portal returns the console's documents (see PortalDocs) as JSON, read
// through one short console session opened with this device's login. A
// document that fails to load is left out and named in errs, so one broken
// page does not blank the others.
func (e *Engine) Portal() (docs map[string]json.RawMessage, errs map[string]string, err error) {
	path, err := e.API.AdminLink()
	if err != nil {
		return nil, nil, err
	}
	cookie, err := e.consoleSession(path)
	if err != nil {
		return nil, nil, err
	}
	web := NewAPI(e.API.Base, "", e.API.HC)
	hdr := map[string]string{"Cookie": cookie, "X-Hearth-Admin": "1"}
	docs, errs = map[string]json.RawMessage{}, map[string]string{}
	for k, p := range PortalDocs {
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
