package server

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// Intents: the console cannot change the user's library itself (the
// machines own it and resolve conflicts), so it queues the change; the
// user's daemon picks it up at its next heartbeat (about a minute), applies
// it and pushes the library.

var intentKinds = map[string]bool{"link": true, "unlink": true, "resident": true, "share-top": true}

type intent struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	CreatedAt int64           `json:"created_at"`
}

func (s *Server) pendingIntents(uid int64) ([]intent, error) {
	rows, err := s.st.rdb.Query(`SELECT id, kind, data, created_at FROM user_intents WHERE user_id=? ORDER BY id LIMIT 100`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []intent{}
	for rows.Next() {
		var it intent
		var data string
		if err := rows.Scan(&it.ID, &it.Kind, &data, &it.CreatedAt); err != nil {
			return nil, err
		}
		it.Data = json.RawMessage(data)
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Server) countIntents(uid int64) int {
	var n int
	_ = s.st.rdb.QueryRow(`SELECT COUNT(*) FROM user_intents WHERE user_id=?`, uid).Scan(&n)
	return n
}

// apiAddIntent (console): queue a change for the user's machines.
func (s *Server) apiAddIntent(w http.ResponseWriter, r *http.Request, c userCtx) {
	var req struct {
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !intentKinds[req.Kind] || len(req.Data) == 0 || len(req.Data) > 16<<10 || !json.Valid(req.Data) {
		writeErr(w, http.StatusBadRequest, "unknown change")
		return
	}
	if s.countIntents(c.uid) >= 100 {
		writeErr(w, http.StatusTooManyRequests, "too many changes waiting for your machines; start hearth on one of them")
		return
	}
	if _, err := s.st.db.Exec(`INSERT INTO user_intents(user_id,kind,data,created_at) VALUES(?,?,?,?)`, c.uid, req.Kind, string(req.Data), s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"pending": s.countIntents(c.uid)})
}

func (s *Server) apiMyIntents(w http.ResponseWriter, _ *http.Request, c userCtx) {
	it, err := s.pendingIntents(c.uid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) apiCancelIntent(w http.ResponseWriter, r *http.Request, c userCtx) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if _, err := s.st.db.Exec(`DELETE FROM user_intents WHERE user_id=? AND id=?`, c.uid, id); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Client side (bearer token): GET /v1/intents, DELETE /v1/intents/{id}.
func (s *Server) handleListIntents(w http.ResponseWriter, r *http.Request) {
	it, err := s.pendingIntents(userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) handleDoneIntent(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if _, err := s.st.db.Exec(`DELETE FROM user_intents WHERE user_id=? AND id=?`, userOf(r), id); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
