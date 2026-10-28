package client

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hearth-project/hearth-cli/internal/pathmap"
)

// fileState tracks how much of one local session file the server has
// confirmed. It is only a cache: after a crash the server's committed size
// is authoritative (design doc 6.5).
type fileState struct {
	Logical  string `json:"logical"`
	Gen      int64  `json:"generation"`
	Uploaded int64  `json:"uploaded"`
	HeadHash string `json:"head_hash"` // sha256 of the first <=4KiB already uploaded
	TailHash string `json:"tail_hash"` // sha256 of the last <=4KiB already uploaded
	Gone     bool   `json:"gone,omitempty"`
	// MetaHash is the thread metadata last sent (see updateMeta).
	MetaHash  string `json:"meta_hash,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	// With path translation (see translated), Uploaded counts server
	// (origin-form) bytes and Local the local bytes they correspond to;
	// Partial is how much of the batch starting at Local is already on the
	// server (a batch can span several chunks).
	Local   int64 `json:"local,omitempty"`
	Partial int64 `json:"partial,omitempty"`
	// Restored marks files written by restore from this account (always
	// ours, whatever their working directory).
	Restored bool `json:"restored,omitempty"`
	// Deleted: the user deleted it in the agent; the server copy was
	// marked expired (with Gone set, it is not uploaded again).
	Deleted bool `json:"deleted,omitempty"`
	// Path is where the file was last seen on this machine; deletion is
	// only concluded for files seen under the current agent directory.
	Path string `json:"path,omitempty"`
	// SessionStats is the searchable index of the conversation.
	SessionStats
}

// remoteInfo is how a session restored from another machine maps onto this
// one. It survives resets of the fileState (re-registration).
type remoteInfo struct {
	Map pathmap.Map `json:"map"` // origin (and other known workspaces) -> local
	// Back translates appended local lines to the server form (origin ->
	// local inverted, without the extra workspaces); unset in old state.
	Back    pathmap.Map `json:"back,omitempty"`
	BackSet bool        `json:"back_set,omitempty"`
	Meta    SessionMeta `json:"meta"` // metadata as the origin wrote it
}

type stateFile struct {
	Sessions map[string]*fileState `json:"sessions"`
	// Remote: sid -> translation; Aliases: local logical path -> sid, for
	// files restored under a different name (Claude project directories).
	Remote  map[string]*remoteInfo `json:"remote,omitempty"`
	Aliases map[string]string      `json:"aliases,omitempty"`
}

type state struct {
	mu   sync.Mutex
	path string
	data stateFile
	// stamp is state.json as this process last read or wrote it; another
	// process (CLI restore vs daemon) changing it triggers a reload.
	stamp fileStamp
	// gone: keys this process deleted, not to be merged back from disk.
	gone map[string]bool
}

// save writes the state (caller holds mu). Entries another process added
// since this one last read the file are kept (this process wins on the
// entries both have).
func (s *state) save() error {
	if fi, err := os.Stat(s.path); err == nil && (fileStamp{fi.Size(), fi.ModTime()}) != s.stamp {
		if disk, err := loadState(filepath.Dir(s.path)); err == nil {
			for k, v := range disk.data.Sessions {
				if _, ok := s.data.Sessions[k]; !ok && !s.gone["s:"+k] {
					s.data.Sessions[k] = v
				}
			}
			for k, v := range disk.data.Remote {
				if _, ok := s.data.Remote[k]; !ok && !s.gone["r:"+k] {
					s.data.Remote[k] = v
				}
			}
			for k, v := range disk.data.Aliases {
				if _, ok := s.data.Aliases[k]; !ok && !s.gone["a:"+k] {
					s.data.Aliases[k] = v
				}
			}
		}
	}
	if err := saveJSON(s.path, &s.data); err != nil {
		return err
	}
	if fi, err := os.Stat(s.path); err == nil {
		s.stamp = fileStamp{fi.Size(), fi.ModTime()}
	}
	return nil
}

func (s *state) forget(key string) {
	if s.gone == nil {
		s.gone = map[string]bool{}
	}
	s.gone[key] = true
}

// refresh reloads state.json if another process wrote it since.
func (s *state) refresh() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fi, err := os.Stat(s.path)
	if err != nil || (fileStamp{fi.Size(), fi.ModTime()}) == s.stamp {
		return nil
	}
	fresh, err := loadState(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	s.data, s.stamp = fresh.data, fresh.stamp
	return nil
}

func loadState(home string) (*state, error) {
	s := &state{path: filepath.Join(home, "state.json"), data: stateFile{Sessions: map[string]*fileState{}}}
	if err := loadJSON(s.path, &s.data); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if s.data.Sessions == nil {
		s.data.Sessions = map[string]*fileState{}
	}
	if s.data.Remote == nil {
		s.data.Remote = map[string]*remoteInfo{}
	}
	if s.data.Aliases == nil {
		s.data.Aliases = map[string]string{}
	}
	if fi, err := os.Stat(s.path); err == nil {
		s.stamp = fileStamp{fi.Size(), fi.ModTime()}
	}
	return s, nil
}

func (s *state) remote(sid string) *remoteInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Remote[sid]
}

func (s *state) alias(logical string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Aliases[logical]
}

// setRemote records (or with ri == nil clears) the translation of sid and
// the local name it is restored under.
func (s *state) setRemote(sid, localLogical string, ri *remoteInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for l, x := range s.data.Aliases {
		if x == sid {
			delete(s.data.Aliases, l)
			s.forget("a:" + l)
		}
	}
	if ri == nil {
		delete(s.data.Remote, sid)
		s.forget("r:" + sid)
	} else {
		s.data.Remote[sid] = ri
		if localLogical != "" && localLogical != ri.Meta.Agent+"/"+ri.Meta.RelPath {
			s.data.Aliases[localLogical] = sid
		}
	}
	return s.save()
}

// codexMoved finds the session of a Codex rollout that Codex moved between
// sessions/YYYY/MM/DD/ and archived_sessions/ (archive, unarchive): the file
// name (which carries the thread id) stays the same.
func (s *state) codexMoved(logical string) string {
	base := path.Base(logical)
	if !strings.HasPrefix(logical, "codex/") || !strings.HasPrefix(base, "rollout-") {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for l, sid := range s.data.Aliases {
		if path.Base(l) == base && strings.HasPrefix(l, "codex/") {
			return sid
		}
	}
	for sid, fs := range s.data.Sessions {
		if fs != nil && path.Base(fs.Logical) == base && strings.HasPrefix(fs.Logical, "codex/") {
			return sid
		}
	}
	return ""
}

// addAlias records that a local file is session sid.
func (s *state) addAlias(logical, sid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Aliases[logical] = sid
	return s.save()
}

// all returns a copy of every tracked session.
func (s *state) all() map[string]fileState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]fileState, len(s.data.Sessions))
	for k, v := range s.data.Sessions {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

func (s *state) get(sid string) *fileState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Sessions[sid]
}

func (s *state) put(sid string, fs *fileState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fs == nil {
		delete(s.data.Sessions, sid)
		s.forget("s:" + sid)
	} else {
		s.data.Sessions[sid] = fs
	}
	return s.save()
}

const hashWindow = 4096

// prefixHashes returns hashes of the head and tail windows of f[0:n].
func prefixHashes(f *os.File, n int64) (head, tail string, err error) {
	read := func(off, l int64) (string, error) {
		b := make([]byte, l)
		if _, err := f.ReadAt(b, off); err != nil && err != io.EOF {
			return "", err
		}
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:]), nil
	}
	hl := min(n, hashWindow)
	if head, err = read(0, hl); err != nil {
		return
	}
	tail, err = read(n-hl, hl)
	return
}
