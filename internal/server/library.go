package server

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

type libraryRow struct {
	Version int64
	SHA256  string
	Object  string
}

func (s *store) getLibrary(uid int64) (libraryRow, error) {
	var r libraryRow
	err := s.rdb.QueryRow(`SELECT version, sha256, object FROM libraries WHERE user_id=?`, uid).Scan(&r.Version, &r.SHA256, &r.Object)
	if errors.Is(err, sql.ErrNoRows) {
		return r, errNotFound
	}
	return r, err
}

// putLibrary stores a new bundle if base matches the current version
// (0 = no library yet). Old bundles are removed after commit.
func (s *store) putLibrary(uid int64, device string, base int64, data []byte, sum string, now int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var cur int64
	var oldObj string
	err = tx.QueryRow(`SELECT version, object FROM libraries WHERE user_id=?`+s.d.forUpdate(), uid).Scan(&cur, &oldObj)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if base != cur {
		return cur, &conflictError{Generation: cur}
	}
	next := cur + 1
	// A unique name: a concurrent first upload that loses the race must not
	// overwrite the winner's object.
	obj := fmt.Sprintf("%d/library/%d-%x.tar.gz", uid, next, crypto.RandomBytes(4))
	if err := s.blobs.put(obj, data); err != nil {
		return 0, err
	}
	if cur == 0 {
		_, err = tx.Exec(`INSERT INTO libraries(user_id,version,sha256,object,size_bytes,updated_by,updated_at) VALUES(?,?,?,?,?,?,?)`,
			uid, next, sum, obj, len(data), device, now)
	} else {
		_, err = tx.Exec(`UPDATE libraries SET version=?, sha256=?, object=?, size_bytes=?, updated_by=?, updated_at=? WHERE user_id=? AND version=?`,
			next, sum, obj, len(data), device, now, uid, cur)
	}
	if err != nil {
		s.blobs.del(obj)
		if isUnique(err) {
			return cur, &conflictError{Generation: cur}
		}
		return 0, err
	}
	if oldObj != "" {
		if _, err := tx.Exec(`INSERT INTO pending_deletes(object) VALUES(?) ON CONFLICT DO NOTHING`, oldObj); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, s.flushDeletes()
}

func (s *Server) handleGetLibrary(w http.ResponseWriter, r *http.Request) {
	row, err := s.st.getLibrary(userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.Header().Set(api.HeaderLibraryVersion, strconv.FormatInt(row.Version, 10))
	w.Header().Set(api.HeaderLibrarySHA256, row.SHA256)
	// Cheap polling: the client sends the version it already has.
	if r.Header.Get(api.HeaderLibraryVersion) == strconv.FormatInt(row.Version, 10) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, err := s.st.readObject(row.Object)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Write(data)
}

func (s *Server) handlePutLibrary(w http.ResponseWriter, r *http.Request) {
	base, ok := parseInt(r.Header.Get(api.HeaderLibraryVersion))
	sum := r.Header.Get(api.HeaderLibrarySHA256)
	if !ok || len(sum) != 64 {
		writeErr(w, http.StatusBadRequest, "missing base version or sha256")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.MaxLibraryBytes))
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusRequestEntityTooLarge, "library bundle too large or empty")
		return
	}
	// sum identifies the bundle content; it is computed and verified by
	// clients, the server only uses it to answer "has anything changed".
	var old int64
	s.st.rdb.QueryRow(`SELECT size_bytes FROM libraries WHERE user_id=?`, userOf(r)).Scan(&old)
	delta := int64(len(data)) - old
	if err := s.reserve(userOf(r), delta); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	v, err := s.st.putLibrary(userOf(r), deviceOf(r), base, data, sum, s.now())
	if err != nil {
		s.reserve(userOf(r), -delta)
	}
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.Header().Set(api.HeaderLibraryVersion, strconv.FormatInt(v, 10))
	writeJSON(w, http.StatusOK, map[string]int64{"version": v})
}
