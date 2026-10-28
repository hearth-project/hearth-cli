package server

import (
	"database/sql"
	"net/http"

	"github.com/hearth-project/hearth-cli/internal/api"
)

func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.rdb.Query(`SELECT secret_id, ciphertext, updated_at FROM secrets WHERE user_id=? ORDER BY secret_id`, userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	defer rows.Close()
	out := []api.Secret{}
	for rows.Next() {
		var x api.Secret
		if err := rows.Scan(&x.ID, &x.Ciphertext, &x.UpdatedAt); err != nil {
			s.writeStoreErr(w, err)
			return
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutSecret(w http.ResponseWriter, r *http.Request) {
	var req api.PutSecretRequest
	r.Body = http.MaxBytesReader(w, r.Body, api.MaxSecretBytes*2)
	if !readJSON(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if !validSessionID(id) || len(req.Ciphertext) == 0 || len(req.Ciphertext) > api.MaxSecretBytes+1024 {
		writeErr(w, http.StatusBadRequest, "invalid secret id or size")
		return
	}
	uid := userOf(r)
	var n int
	var exists int
	s.st.db.QueryRow(`SELECT COUNT(*) FROM secrets WHERE user_id=?`, uid).Scan(&n)
	err := s.st.db.QueryRow(`SELECT 1 FROM secrets WHERE user_id=? AND secret_id=?`, uid, id).Scan(&exists)
	if err == sql.ErrNoRows && n >= api.MaxSecretsPerUser {
		writeErr(w, http.StatusInsufficientStorage, "too many secrets")
		return
	}
	if err == sql.ErrNoRows { // updates are bounded by MaxSecretBytes; count new secrets only
		if err := s.reserve(uid, int64(len(req.Ciphertext))); err != nil {
			s.writeStoreErr(w, err)
			return
		}
	}
	if _, err := s.st.db.Exec(`INSERT INTO secrets(user_id,secret_id,ciphertext,updated_by,updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(user_id,secret_id) DO UPDATE SET ciphertext=excluded.ciphertext, updated_by=excluded.updated_by, updated_at=excluded.updated_at`,
		uid, id, req.Ciphertext, deviceOf(r), s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	res, err := s.st.db.Exec(`DELETE FROM secrets WHERE user_id=? AND secret_id=?`, userOf(r), r.PathValue("id"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
