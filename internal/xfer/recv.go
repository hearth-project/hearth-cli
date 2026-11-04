package xfer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Options of a copy.
type Options struct {
	Streams   int  `json:"streams,omitempty"`    // data connections (0 = by latency)
	Compress  bool `json:"compress,omitempty"`   // zstd per chunk, kept only where it helps
	NoClobber bool `json:"no_clobber,omitempty"` // keep files that exist at the target
	Force     bool `json:"force,omitempty"`      // copy files even when size and time match
}

// FilePlan: a file the receiver needs; Chunks nil = all of it.
type FilePlan struct {
	I      int     `json:"i"`
	Chunks []int32 `json:"c,omitempty"`
}

// Plan is the receiver's answer to a manifest.
type Plan struct {
	Target  string     `json:"target"`
	Files   []FilePlan `json:"files"`
	Skipped int        `json:"skipped"`        // up to date (size and time) or kept (no-clobber)
	Resumed int        `json:"resumed"`        // partly there from an earlier attempt
	Bytes   int64      `json:"bytes"`          // to transfer
	Kept    []string   `json:"kept,omitempty"` // no-clobber: existing files left as they are
}

type rfile struct {
	e         Entry
	final     string
	part      string
	mu        sync.Mutex
	f         *os.File // opened at the first chunk, closed when complete
	direct    *os.File // O_DIRECT on Linux for large files (parallel writes)
	resume    bool
	finished  bool
	done      []byte // chunk bitmap
	total     int
	remaining atomic.Int32
	dirty     bool
}

// Receiver writes a manifest's files under its target.
type Receiver struct {
	m       *Manifest
	target  string
	files   map[int]*rfile
	written atomic.Int64
	pending atomic.Int32 // files not finished
	errMu   sync.Mutex
	err     error
	doneCh  chan struct{}
	stop    chan struct{}
	stopped sync.Once
	closed  sync.Once
	ok      bool // every file in place
}

func nchunks(size int64) int { return int((size + ChunkSize - 1) / ChunkSize) }

type sidecar struct {
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
	Chunk int    `json:"chunk"`
	Done  []byte `json:"done"`
}

func sameTime(a, b int64) bool { return a/1e9 == b/1e9 }

// NewReceiver prepares the target for m (directories, part files) and
// returns the plan: what has to come.
func NewReceiver(dst string, m *Manifest, o Options) (*Receiver, *Plan, error) {
	for _, e := range m.Entries {
		if !safeRel(e.Path) {
			return nil, nil, fmt.Errorf("unsafe path in the manifest: %q", e.Path)
		}
	}
	target, err := Target(dst, m)
	if err != nil {
		return nil, nil, err
	}
	r := &Receiver{m: m, target: target, files: map[int]*rfile{}, doneCh: make(chan struct{}), stop: make(chan struct{})}
	p := &Plan{Target: target}
	for _, e := range m.Entries {
		if e.Kind == 'd' {
			if err := os.MkdirAll(r.path(e), 0o755); err != nil {
				return nil, nil, err
			}
		}
	}
	for i, e := range m.Entries {
		if e.Kind != 'f' {
			continue
		}
		final := r.path(e)
		if fi, err := os.Lstat(final); err == nil {
			if o.NoClobber {
				p.Skipped++
				p.Kept = append(p.Kept, final)
				continue
			}
			if !o.Force && fi.Mode().IsRegular() && fi.Size() == e.Size && sameTime(fi.ModTime().UnixNano(), e.MTime) {
				p.Skipped++
				continue
			}
		}
		rf := &rfile{e: e, final: final, part: filepath.Join(filepath.Dir(final), "."+filepath.Base(final)+".hearth-part"), total: nchunks(e.Size)}
		rf.done = make([]byte, (rf.total+7)/8)
		var need []int32
		resumed := false
		if b, err := os.ReadFile(rf.part + ".json"); err == nil {
			var sc sidecar
			if json.Unmarshal(b, &sc) == nil && sc.Size == e.Size && sc.MTime == e.MTime && sc.Chunk == ChunkSize && len(sc.Done) == len(rf.done) {
				if fi, err := os.Stat(rf.part); err == nil && fi.Size() == e.Size {
					rf.done, resumed = sc.Done, true
				}
			}
		}
		rf.resume = resumed
		for c := 0; c < rf.total; c++ {
			if rf.done[c/8]&(1<<(c%8)) == 0 {
				need = append(need, int32(c))
				p.Bytes += chunkLen(e.Size, c)
			}
		}
		rf.remaining.Store(int32(len(need)))
		r.files[i] = rf
		if resumed {
			p.Resumed++
		}
		if len(need) == 0 {
			if err := r.finish(rf); err != nil {
				return nil, nil, err
			}
			continue
		}
		r.pending.Add(1)
		fp := FilePlan{I: i}
		if len(need) != rf.total {
			fp.Chunks = need
		}
		p.Files = append(p.Files, fp)
	}
	if r.pending.Load() == 0 {
		if err := r.finalize(); err != nil {
			return nil, nil, err
		}
		r.ok = true
		r.closed.Do(func() { close(r.doneCh) })
	}
	go r.flushLoop()
	return r, p, nil
}

func chunkLen(size int64, c int) int64 {
	if rest := size - int64(c)*ChunkSize; rest < ChunkSize {
		return rest
	}
	return ChunkSize
}

func (r *Receiver) path(e Entry) string {
	if e.Path == "" {
		return r.target
	}
	return filepath.Join(r.target, filepath.FromSlash(e.Path))
}

// Target is where the copy lands.
func (r *Receiver) Target() string { return r.target }

// Written counts bytes written so far.
func (r *Receiver) Written() int64 { return r.written.Load() }

// Done is closed when every file is in place (or on the first error).
func (r *Receiver) Done() <-chan struct{} { return r.doneCh }

// Err is the first error.
func (r *Receiver) Err() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.err
}

func (r *Receiver) fail(err error) {
	r.errMu.Lock()
	if r.err == nil && !r.ok {
		r.err = err
	}
	r.errMu.Unlock()
	r.closed.Do(func() { close(r.doneCh) })
}

// open creates (or, resuming, reopens) the part file at the first chunk;
// files are opened only while chunks arrive, so a million small files do
// not need a million descriptors.
func (rf *rfile) open() error {
	if rf.f != nil || rf.finished {
		return nil
	}
	flags := os.O_RDWR | os.O_CREATE
	if !rf.resume {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(rf.part, flags, 0o600)
	if err != nil {
		return err
	}
	if rf.e.Size >= 16*ChunkSize {
		rf.direct = openDirect(rf.part)
	}
	if !rf.resume {
		// Blocks reserved only for direct writes (into holes they are slow);
		// tmpfs and buffered writes get the size alone (no zeroing up front).
		err := f.Truncate(rf.e.Size)
		if rf.direct != nil {
			err = allocate(f, rf.e.Size)
		}
		if err != nil {
			f.Close()
			return err
		}
	}
	rf.f = f
	return nil
}

// WriteChunk stores one chunk of file i at off.
func (r *Receiver) WriteChunk(i int, off int64, data []byte) error {
	rf := r.files[i]
	if rf == nil || off < 0 || off%ChunkSize != 0 || off+int64(len(data)) > rf.e.Size {
		return errors.New("a chunk for a file or range not asked for")
	}
	c := int(off / ChunkSize)
	if int64(len(data)) != chunkLen(rf.e.Size, c) {
		return errors.New("a chunk of the wrong length")
	}
	rf.mu.Lock()
	already := rf.done[c/8]&(1<<(c%8)) != 0
	err := rf.open()
	f, direct := rf.f, rf.direct
	rf.mu.Unlock()
	if already {
		return nil
	}
	if err != nil {
		return err
	}
	if direct != nil && len(data) == ChunkSize {
		if err := writeDirect(direct, data, off); err != nil {
			rf.mu.Lock()
			if rf.direct == direct {
				rf.direct = nil // not supported here: buffered from now on
				direct.Close()
			}
			rf.mu.Unlock()
			direct = nil
		}
	}
	if direct == nil || len(data) != ChunkSize {
		if _, err := f.WriteAt(data, off); err != nil {
			return err
		}
	}
	rf.mu.Lock()
	rf.done[c/8] |= 1 << (c % 8)
	rf.dirty = true
	rf.mu.Unlock()
	r.written.Add(int64(len(data)))
	if rf.remaining.Add(-1) == 0 {
		if err := r.finish(rf); err != nil {
			return err
		}
		if r.pending.Add(-1) == 0 {
			if err := r.finalize(); err != nil {
				return err
			}
			r.errMu.Lock()
			if r.err == nil {
				r.ok = true
			}
			r.errMu.Unlock()
			r.closed.Do(func() { close(r.doneCh) })
		}
	}
	return nil
}

// finish puts a complete file in place: mode, time, rename.
func (r *Receiver) finish(rf *rfile) error {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.finished {
		return nil
	}
	if rf.direct != nil {
		rf.direct.Close()
		rf.direct = nil
	}
	if rf.f != nil {
		if err := rf.f.Close(); err != nil {
			return err
		}
		rf.f = nil
	} else if rf.e.Size == 0 {
		// an empty file has no chunks: create it now
		if err := os.WriteFile(rf.part, nil, 0o600); err != nil {
			return err
		}
	}
	rf.finished = true
	t := time.Unix(0, rf.e.MTime)
	_ = os.Chmod(rf.part, rf.e.Mode.Perm())
	_ = os.Chtimes(rf.part, t, t)
	if fi, err := os.Lstat(rf.final); err == nil && fi.IsDir() {
		return fmt.Errorf("%s is a directory", rf.final)
	}
	if err := os.Rename(rf.part, rf.final); err != nil {
		return err
	}
	os.Remove(rf.part + ".json")
	return nil
}

// finalize: symlinks, then directory modes and times (deepest first).
func (r *Receiver) finalize() error {
	r.stopped.Do(func() { close(r.stop) })
	for _, e := range r.m.Entries {
		if e.Kind != 'l' {
			continue
		}
		p := r.path(e)
		if cur, err := os.Readlink(p); err == nil && cur == e.Link {
			continue
		}
		os.Remove(p)
		if err := os.Symlink(e.Link, p); err != nil {
			return err
		}
	}
	var dirs []Entry
	for _, e := range r.m.Entries {
		if e.Kind == 'd' {
			dirs = append(dirs, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].Path) > len(dirs[j].Path) })
	for _, e := range dirs {
		p := r.path(e)
		_ = os.Chmod(p, e.Mode.Perm())
		t := time.Unix(0, e.MTime)
		_ = os.Chtimes(p, t, t)
	}
	return nil
}

// flushLoop saves the chunks received so far, so an interrupted copy
// resumes where it stopped.
func (r *Receiver) flushLoop() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			r.Flush()
		}
	}
}

// Flush writes the resume state of unfinished files.
func (r *Receiver) Flush() {
	for _, rf := range r.files {
		rf.mu.Lock()
		// No fsync: the state covers an interrupted copy (the written
		// chunks are in the page cache), not a power loss.
		if rf.f != nil && rf.dirty {
			b, _ := json.Marshal(sidecar{Size: rf.e.Size, MTime: rf.e.MTime, Chunk: ChunkSize, Done: rf.done})
			if os.WriteFile(rf.part+".json.tmp", b, 0o600) == nil {
				os.Rename(rf.part+".json.tmp", rf.part+".json")
			}
			rf.dirty = false
		}
		rf.mu.Unlock()
	}
}

// Abort saves the resume state and closes the part files.
func (r *Receiver) Abort() {
	r.stopped.Do(func() { close(r.stop) })
	r.Flush()
	for _, rf := range r.files {
		rf.mu.Lock()
		if rf.direct != nil {
			rf.direct.Close()
			rf.direct = nil
		}
		if rf.f != nil {
			rf.f.Close()
			rf.f = nil
		}
		rf.mu.Unlock()
	}
}
