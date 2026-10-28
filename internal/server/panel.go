package server

import (
	"embed"
	"net/http"
	"path"
)

// The admin panel: a static page (no build step, no external assets) that
// talks to /v1/admin with the session cookie.

//go:embed panel/*
var panelFS embed.FS

var panelTypes = map[string]string{".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8"}

func panelHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

func (s *Server) handlePanel(w http.ResponseWriter, _ *http.Request) {
	b, err := panelFS.ReadFile("panel/index.html")
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	panelHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

func (s *Server) handlePanelAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	ct, ok := panelTypes[path.Ext(name)]
	b, err := panelFS.ReadFile("panel/" + name)
	if !ok || err != nil {
		http.NotFound(w, r)
		return
	}
	panelHeaders(w)
	w.Header().Set("Content-Type", ct)
	w.Write(b)
}
