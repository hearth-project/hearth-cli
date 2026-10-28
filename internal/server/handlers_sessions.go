package server

import (
	"encoding/binary"
	"io"
	"net/http"
	"strconv"

	"github.com/hearth-project/hearth-cli/internal/api"
)

func toAPI(r sessionRow) api.Session { return api.Session(r) }

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit < 0 || offset < 0 {
		writeErr(w, http.StatusBadRequest, "bad limit or offset")
		return
	}
	rows, total, err := s.st.listSessions(userOf(r), limit, offset)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.Header().Set(api.HeaderTotalCount, strconv.Itoa(total))
	out := make([]api.Session, 0, len(rows))
	for _, x := range rows {
		out = append(out, toAPI(x))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	row, err := s.st.getSession(userOf(r), r.PathValue("sid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPI(row))
}

func (s *Server) handlePutSession(w http.ResponseWriter, r *http.Request) {
	var req api.PutSessionRequest
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.EncMeta) == 0 || len(req.EncMeta) > 64<<10 {
		writeErr(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	uid, sid := userOf(r), r.PathValue("sid")
	if err := s.st.upsertSession(uid, sid, req.EncMeta, deviceOf(r), s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.handleGetSession(w, r)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.st.deleteSession(userOf(r), r.PathValue("sid"), s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTouch(w http.ResponseWriter, r *http.Request) {
	if err := s.st.touch(userOf(r), r.PathValue("sid"), s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.handleGetSession(w, r)
}

// handleExpire marks a session deleted by the user in their agent: it is
// no longer restored and is removed after the normal grace period.
func (s *Server) handleExpire(w http.ResponseWriter, r *http.Request) {
	if err := s.st.expireSession(userOf(r), r.PathValue("sid"), s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.handleGetSession(w, r)
}

func (s *Server) handlePin(w http.ResponseWriter, r *http.Request) {
	var req api.PinRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.st.setPinned(userOf(r), r.PathValue("sid"), req.Pinned, s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.handleGetSession(w, r)
}

func (s *Server) handleRevive(w http.ResponseWriter, r *http.Request) {
	if err := s.st.revive(userOf(r), r.PathValue("sid")); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if _, err := s.st.resetSession(userOf(r), r.PathValue("sid"), deviceOf(r), s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.handleGetSession(w, r)
}

func (s *Server) handleLease(w http.ResponseWriter, r *http.Request) {
	var req api.LeaseRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.TTLSeconds <= 0 || req.TTLSeconds > 3600 {
		req.TTLSeconds = 300
	}
	uid, sid := userOf(r), r.PathValue("sid")
	row, err := s.st.acquireLease(uid, sid, deviceOf(r), req.TTLSeconds, req.Force, s.now())
	if err == errLocked {
		writeJSON(w, http.StatusLocked, api.Error{Error: "session leased by another device", LeaseOwner: row.LeaseDevice})
		return
	} else if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPI(row))
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	if err := s.st.releaseLease(userOf(r), r.PathValue("sid"), deviceOf(r)); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListChunks(w http.ResponseWriter, r *http.Request) {
	uid, sid := userOf(r), r.PathValue("sid")
	row, err := s.st.getSession(uid, sid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	cs, err := s.st.listChunks(uid, sid, row.Generation)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	out := make([]api.Chunk, 0, len(cs))
	for _, c := range cs {
		out = append(out, api.Chunk{Generation: c.Generation, Offset: c.Offset, Length: c.Length})
	}
	w.Header().Set(api.HeaderGeneration, strconv.FormatInt(row.Generation, 10))
	w.Header().Set(api.HeaderSize, strconv.FormatInt(row.Size, 10))
	writeJSON(w, http.StatusOK, out)
}

func parseInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 0
}

func (s *Server) handleAppendChunk(w http.ResponseWriter, r *http.Request) {
	offset, ok1 := parseInt(r.PathValue("offset"))
	gen, ok2 := parseInt(r.Header.Get(api.HeaderGeneration))
	plen, ok3 := parseInt(r.Header.Get(api.HeaderPlainLength))
	if !ok1 || !ok2 || !ok3 || plen == 0 || plen > api.MaxChunkBytes {
		writeErr(w, http.StatusBadRequest, "invalid offset, generation or length")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.MaxChunkBytes+1024))
	if err != nil || len(body) == 0 {
		writeErr(w, http.StatusRequestEntityTooLarge, "chunk too large or empty")
		return
	}
	if err := s.reserve(userOf(r), plen); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	row, err := s.st.appendChunk(userOf(r), r.PathValue("sid"), deviceOf(r), gen, offset, plen, body, s.now())
	if err == nil {
		s.activity.upload(s.cfg.Now(), userOf(r), plen)
	}
	if err != nil {
		s.reserve(userOf(r), -plen)
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPI(row))
}

func (s *Server) handleGetChunk(w http.ResponseWriter, r *http.Request) {
	offset, ok1 := parseInt(r.PathValue("offset"))
	gen, ok2 := parseInt(r.URL.Query().Get("generation"))
	if !ok1 || !ok2 {
		writeErr(w, http.StatusBadRequest, "invalid offset or generation")
		return
	}
	uid, sid := userOf(r), r.PathValue("sid")
	obj, err := s.st.chunkObject(uid, sid, gen, offset)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	data, err := s.st.readObject(obj)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	// Downloading counts as access (design doc 6.8).
	_ = s.st.touch(uid, sid, s.now())
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

// handleSessionContent streams every chunk of a generation in one response,
// so restoring a session costs one round trip instead of one per chunk.
// Frame: offset uint64 | plain length uint32 | ciphertext length uint32 | ciphertext.
func (s *Server) handleSessionContent(w http.ResponseWriter, r *http.Request) {
	gen, ok := parseInt(r.URL.Query().Get("generation"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid generation")
		return
	}
	uid, sid := userOf(r), r.PathValue("sid")
	chunks, err := s.st.listChunks(uid, sid, gen)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if len(chunks) == 0 {
		if _, err := s.st.getSession(uid, sid); err != nil {
			s.writeStoreErr(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/x-hearth-chunks")
	// Fetch blobs ahead of the writer (object stores have per-request
	// latency) while keeping the output in order and memory bounded
	// (about 64 MiB in flight, 4 to 32 blobs).
	var total int64
	for _, c := range chunks {
		total += c.Length
	}
	ahead := 32
	if len(chunks) > 0 {
		ahead = min(32, max(4, int((64<<20)/(total/int64(len(chunks))+1))))
	}
	type res struct {
		data []byte
		err  error
	}
	slots := make([]chan res, len(chunks))
	for i := range slots {
		slots[i] = make(chan res, 1)
	}
	sem := make(chan struct{}, ahead)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for i, c := range chunks {
			select {
			case sem <- struct{}{}:
			case <-done:
				return
			}
			go func() {
				d, err := s.st.readObject(c.Object)
				slots[i] <- res{d, err}
			}()
		}
	}()
	var hdr [16]byte
	for i, c := range chunks {
		r := <-slots[i]
		<-sem
		if r.err != nil {
			// Headers are already sent; abort the stream so the client sees
			// a truncated body and its length checks fail.
			s.cfg.Logger.Printf("content %s: %v", c.Object, r.err)
			panic(http.ErrAbortHandler)
		}
		binary.BigEndian.PutUint64(hdr[0:8], uint64(c.Offset))
		binary.BigEndian.PutUint32(hdr[8:12], uint32(c.Length))
		binary.BigEndian.PutUint32(hdr[12:16], uint32(len(r.data)))
		w.Write(hdr[:])
		w.Write(r.data)
	}
	_ = s.st.touch(uid, sid, s.now())
}
