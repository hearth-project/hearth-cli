package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/hearth-project/hearth-cli/internal/api"
)

// handleTransferConfig serves <data>/transfer.json to logged-in users: the
// transfer service address, its shared token and the path rules, so clients
// need no settings of their own (`hearth transfer`). It is not part of the
// site bundle, which is public by default. Read on every request, so edits
// apply at once; the file is small and the route needs a login.
func (s *Server) handleTransferConfig(w http.ResponseWriter, _ *http.Request) {
	b, err := os.ReadFile(s.cfg.TransferFile)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusNotFound, "no transfer settings on this server")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "transfer settings: cannot read the file")
		return
	}
	var c api.TransferConfig
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields() // a misspelt key is the admin's mistake: say so
	if err := dec.Decode(&c); err != nil {
		writeErr(w, http.StatusInternalServerError, "transfer settings: "+err.Error())
		return
	}
	var rest json.RawMessage
	if err := dec.Decode(&rest); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusInternalServerError, "transfer settings: unexpected data after the JSON object")
		return
	}
	if u, err := url.Parse(c.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		writeErr(w, http.StatusInternalServerError, "transfer settings: url must look like http://host[:port] without a user name or password")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, c)
}
