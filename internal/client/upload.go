package client

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/pathmap"
)

// upload appends file[fs.Uploaded:size] to the server chunk by chunk. The
// local state is saved after every confirmed chunk, and replays after a
// crash are idempotent on the server.
func (e *Engine) upload(sid string, fs *fileState, file *os.File, size int64) (int64, error) {
	var sent int64
	buf := make([]byte, e.ChunkSize)
	for fs.Uploaded < size {
		n := int(min(int64(len(buf)), size-fs.Uploaded))
		got, err := file.ReadAt(buf[:n], fs.Uploaded)
		if err != nil && !(errors.Is(err, io.EOF) && got > 0) {
			return sent, err
		}
		plain := buf[:got]
		ct, err := e.Vault.SealChunk(sid, fs.Gen, fs.Uploaded, plain)
		if err != nil {
			return sent, err
		}
		if _, err := e.API.AppendChunk(sid, fs.Gen, fs.Uploaded, len(plain), ct); err != nil {
			var he *HTTPError
			if errors.As(err, &he) && he.Status == 409 && he.Body.Generation == fs.Gen &&
				he.Body.Size > fs.Uploaded && he.Body.Size <= size {
				// The server is ahead of our cache: an earlier run uploaded
				// these bytes but crashed before saving state. We hold the
				// lease, so they came from this file.
				fs.Uploaded = he.Body.Size
			} else {
				return sent, e.serverErr(sid, fs, err)
			}
		} else {
			fs.Uploaded += int64(len(plain))
			sent += int64(len(plain))
		}
		if fs.HeadHash, fs.TailHash, err = prefixHashes(file, fs.Uploaded); err != nil {
			return sent, err
		}
		if err := e.st.put(sid, fs); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

// uploadTranslated uploads the complete lines appended to a file restored
// from another machine, with this machine's paths rewritten back to the
// origin's (back maps local -> origin). A trailing incomplete line waits for
// its newline, so a path is never split by a translation boundary. The
// translation is deterministic, so a batch interrupted between chunks is
// resumed by recomputing it and skipping the Partial bytes already sent.
func (e *Engine) uploadTranslated(sid string, fs *fileState, file *os.File, size int64, back pathmap.Map) (int64, error) {
	if size <= fs.Local {
		return 0, nil
	}
	local := make([]byte, size-fs.Local)
	got, err := file.ReadAt(local, fs.Local)
	if err != nil && !(errors.Is(err, io.EOF) && got > 0) {
		return 0, err
	}
	local = local[:got]
	cut := bytes.LastIndexByte(local, '\n')
	if cut < 0 {
		return 0, nil
	}
	local = local[:cut+1]
	canon := back.Apply(local)
	base := fs.Uploaded - fs.Partial
	if fs.Partial > int64(len(canon)) {
		fs.Partial = 0 // the file changed under a half-sent batch; the prefix check catches rewrites
		base = fs.Uploaded
	}
	var sent int64
	for off := fs.Partial; off < int64(len(canon)); {
		n := min(int64(e.ChunkSize), int64(len(canon))-off)
		plain := canon[off : off+n]
		ct, err := e.Vault.SealChunk(sid, fs.Gen, base+off, plain)
		if err != nil {
			return sent, err
		}
		if _, err := e.API.AppendChunk(sid, fs.Gen, base+off, len(plain), ct); err != nil {
			var he *HTTPError
			if errors.As(err, &he) && he.Status == 409 && he.Body.Generation == fs.Gen &&
				he.Body.Size > base+off && he.Body.Size <= base+int64(len(canon)) {
				off = he.Body.Size - base // sent by an earlier, interrupted run
			} else {
				return sent, e.serverErr(sid, fs, err)
			}
		} else {
			off += n
			sent += n
		}
		fs.Uploaded, fs.Partial = base+off, off
		if err := e.st.put(sid, fs); err != nil {
			return sent, err
		}
	}
	fs.Local += int64(len(local))
	fs.Uploaded, fs.Partial = base+int64(len(canon)), 0
	if fs.HeadHash, fs.TailHash, err = prefixHashes(file, fs.Local); err != nil {
		return sent, err
	}
	return sent, e.st.put(sid, fs)
}

// serverErr translates server responses that change local state.
func (e *Engine) serverErr(sid string, fs *fileState, err error) error {
	switch StatusOf(err) {
	case 410:
		fs.Gone = true
		_ = e.st.put(sid, fs)
		return fmt.Errorf("session was deleted on the server; not uploading (use `hearth session revive`)")
	case 404:
		// Server lost or never had the row: re-register next pass.
		_ = e.st.put(sid, nil)
		return fmt.Errorf("session unknown to server; will re-register")
	case 409:
		// Generation moved (another device reset it): re-verify next pass.
		_ = e.st.put(sid, nil)
		return fmt.Errorf("server copy changed; will re-verify")
	case 423:
		delete(e.leases, sid)
		return errors.New("session is leased by another device (use `hearth session take`)")
	case 507:
		// Over quota: stop sending data for a while instead of uploading
		// chunks the server will reject on every pass.
		e.quotaUntil = time.Now().Add(QuotaBackoff)
		return fmt.Errorf("storage quota exceeded; uploads paused for %s (hearth status)", QuotaBackoff)
	}
	return err
}

// download fetches and decrypts generation gen of a session in one
// request, verifying the chunks are contiguous and add up to size.
// progressMin: downloads smaller than this finish too fast to report.
const progressMin = 2 << 20

func (e *Engine) download(sid string, gen, size int64) ([]byte, error) {
	var body []byte
	path := fmt.Sprintf("%s/content?generation=%d", sp(sid), gen)
	if e.Progress != nil && size >= progressMin {
		var err error
		body, err = e.API.getCounted(path, func(n int64) { e.Progress(n, size) })
		e.Progress(-1, size) // done
		if err != nil {
			return nil, err
		}
	} else if _, err := e.API.do("GET", path, nil, nil, nil, &body); err != nil {
		return nil, err
	}
	out := make([]byte, 0, size)
	for len(body) > 0 {
		if len(body) < 16 {
			return nil, fmt.Errorf("truncated chunk stream from server")
		}
		off := int64(binary.BigEndian.Uint64(body[0:8]))
		plen := int(binary.BigEndian.Uint32(body[8:12]))
		clen := int(binary.BigEndian.Uint32(body[12:16]))
		body = body[16:]
		if off != int64(len(out)) || plen <= 0 || plen > api.MaxChunkBytes || clen > len(body) {
			return nil, fmt.Errorf("inconsistent chunk stream from server at offset %d", off)
		}
		pt, err := e.Vault.OpenChunk(sid, gen, off, plen, body[:clen])
		if err != nil {
			return nil, fmt.Errorf("chunk at offset %d: %w", off, err)
		}
		out = append(out, pt...)
		body = body[clen:]
	}
	if int64(len(out)) != size {
		return nil, fmt.Errorf("server returned %d bytes, expected %d", len(out), size)
	}
	return out, nil
}

// ReleaseLeases gives up every lease this engine holds, so another machine
// can take over immediately after a graceful shutdown.
func (e *Engine) ReleaseLeases() {
	for sid := range e.leases {
		if err := e.API.ReleaseLease(sid); err != nil {
			e.Logf("release lease: %v", err)
		}
		delete(e.leases, sid)
	}
}
