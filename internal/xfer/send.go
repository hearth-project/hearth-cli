package xfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
)

// job is one chunk to send.
type job struct {
	file int
	off  int64
	n    int64
}

// Sender reads the chunks a plan asks for.
type Sender struct {
	root     string // the source path (a file, or the directory)
	m        *Manifest
	jobs     []job
	sent     atomic.Int64
	compress bool
	noZstd   sync.Map // file index -> true: its chunks do not compress
}

// NewSender checks the plan against the manifest.
func NewSender(src string, m *Manifest, p *Plan, compress bool) (*Sender, error) {
	s := &Sender{root: filepath.Clean(src), m: m, compress: compress}
	// Chunks of several files at once: writers of different files do not
	// wait for each other (writes into one file serialise on its inode).
	// A window of files keeps the open files few.
	const window = 64
	type fileJobs struct {
		js []job
	}
	var cur []fileJobs
	flush := func() {
		for more := true; more; {
			more = false
			for i := range cur {
				if len(cur[i].js) > 0 {
					s.jobs = append(s.jobs, cur[i].js[0])
					cur[i].js = cur[i].js[1:]
					more = true
				}
			}
		}
		cur = cur[:0]
	}
	for _, fp := range p.Files {
		if fp.I < 0 || fp.I >= len(m.Entries) || m.Entries[fp.I].Kind != 'f' {
			return nil, errors.New("the plan names a file that is not in the manifest")
		}
		e := m.Entries[fp.I]
		var fj fileJobs
		add := func(c int) {
			fj.js = append(fj.js, job{file: fp.I, off: int64(c) * ChunkSize, n: chunkLen(e.Size, c)})
		}
		if fp.Chunks == nil {
			for c := 0; c < nchunks(e.Size); c++ {
				add(c)
			}
		} else {
			for _, c := range fp.Chunks {
				if int(c) < 0 || int(c) >= nchunks(e.Size) {
					return nil, errors.New("the plan names a chunk past the end of a file")
				}
				add(int(c))
			}
		}
		cur = append(cur, fj)
		if len(cur) == window {
			flush()
		}
	}
	flush()
	return s, nil
}

// Sent counts bytes read and handed over.
func (s *Sender) Sent() int64 { return s.sent.Load() }

func (s *Sender) file(i int) (*os.File, error) {
	p := s.root
	if rel := s.m.Entries[i].Path; rel != "" {
		p = filepath.Join(s.root, filepath.FromSlash(rel))
	}
	return os.Open(p)
}

// deliver hands one chunk to the receiver: raw is the plain length, zstd
// says data is compressed.
type deliver func(file int, off int64, raw int, zstd bool, data []byte) error

// Run sends every chunk with one worker per deliver function (one per
// data connection); jobs go to whichever worker is free.
func (s *Sender) Run(ctx context.Context, workers []deliver) error {
	ch := make(chan job)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer close(ch)
		for _, j := range s.jobs {
			select {
			case ch <- j:
			case <-ctx.Done():
				return
			}
		}
	}()
	var wg sync.WaitGroup
	var firstErr error
	var once sync.Once
	for _, d := range workers {
		wg.Add(1)
		go func(d deliver) {
			defer wg.Done()
			buf := make([]byte, ChunkSize)
			var enc *zstd.Encoder
			var zbuf []byte
			if s.compress {
				enc, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
				defer enc.Close()
			}
			for j := range ch {
				err := func() error {
					f, err := s.file(j.file)
					if err != nil {
						return err
					}
					b := buf[:j.n]
					_, err = f.ReadAt(b, j.off)
					f.Close()
					if err != nil {
						return err
					}
					if enc != nil {
						if _, no := s.noZstd.Load(j.file); !no {
							zbuf = enc.EncodeAll(b, zbuf[:0])
							if len(zbuf) < len(b)*9/10 {
								if err := d(j.file, j.off, len(b), true, zbuf); err != nil {
									return err
								}
								s.sent.Add(j.n)
								return nil
							}
							s.noZstd.Store(j.file, true)
						}
					}
					if err := d(j.file, j.off, len(b), false, b); err != nil {
						return err
					}
					s.sent.Add(j.n)
					return nil
				}()
				if err != nil {
					once.Do(func() { firstErr = err; cancel() })
					for range ch {
					}
					return
				}
			}
		}(d)
	}
	wg.Wait()
	return firstErr
}

// Local copies on one machine (the same disk, or a disk both machines
// mount): the sender hands chunks straight to the receiver.
func Local(ctx context.Context, src, dst string, o Options, progress func(done, total int64)) (*Plan, error) {
	m, err := Scan(src)
	if err != nil {
		return nil, err
	}
	r, p, err := NewReceiver(dst, m, o)
	if err != nil {
		return nil, err
	}
	s, err := NewSender(src, m, p, false)
	if err != nil {
		r.Abort()
		return nil, err
	}
	n := o.Streams
	if n <= 0 {
		n = 16
	}
	ws := make([]deliver, n)
	for i := range ws {
		ws[i] = func(file int, off int64, raw int, z bool, data []byte) error { return r.WriteChunk(file, off, data) }
	}
	stop := make(chan struct{})
	if progress != nil {
		go tick(stop, func() { progress(r.Written(), p.Bytes) })
	}
	err = s.Run(ctx, ws)
	close(stop)
	if err == nil && len(p.Files) > 0 {
		<-r.Done()
		err = r.Err()
	}
	if err != nil {
		r.Abort()
		return p, err
	}
	return p, nil
}
