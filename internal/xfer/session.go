package xfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

// The control channel (JSON lines over `hearth ssh <machine> -- hearth xfer
// serve`): the local `hearth cp` drives, the remote follows.
//
//	local                                 remote
//	hello {role, path, key, options}  ->
//	                                  <-  ready {addrs, port, abs, marker}
//	shared | noshared                 ->  (shared: the local side copies on the disk both see)
//	the data connections: local dials the remote's addresses; else the remote
//	dials local's (dialme / dialed); else through SSH port forwards (tunnel)
//	conns {n, compress}               ->
//	manifest (from the sender)        <->
//	plan (from the receiver)          <->
//	... chunks over the data connections ...
//	result (from the receiver)        <->

// Msg is one control message.
type Msg struct {
	Op       string    `json:"op"`
	Role     string    `json:"role,omitempty"` // the remote's role: send or recv
	Path     string    `json:"path,omitempty"`
	Abs      string    `json:"abs,omitempty"`
	Key      []byte    `json:"key,omitempty"`
	Opts     *Options  `json:"opts,omitempty"`
	Addrs    []string  `json:"addrs,omitempty"`
	Port     int       `json:"port,omitempty"`
	Marker   string    `json:"marker,omitempty"`
	N        int       `json:"n,omitempty"`
	Compress bool      `json:"compress,omitempty"`
	OK       bool      `json:"ok,omitempty"`
	RTT      int64     `json:"rtt_us,omitempty"`
	Manifest *Manifest `json:"manifest,omitempty"`
	Plan     *Plan     `json:"plan,omitempty"`
	Written  int64     `json:"written,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// Ctrl reads and writes control messages.
type Ctrl struct {
	enc *json.Encoder
	dec *json.Decoder
	mu  sync.Mutex
}

func NewCtrl(r io.Reader, w io.Writer) *Ctrl {
	return &Ctrl{enc: json.NewEncoder(w), dec: json.NewDecoder(r)}
}

func (c *Ctrl) Send(m Msg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(m)
}

// Recv reads the next message; an "error" message becomes an error.
func (c *Ctrl) Recv(want string) (Msg, error) {
	var m Msg
	if err := c.dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return m, errors.New("the other machine ended the transfer")
		}
		return m, err
	}
	if m.Op == "error" {
		return m, errors.New(m.Error)
	}
	if want != "" && m.Op != want {
		return m, fmt.Errorf("unexpected %q (want %q)", m.Op, want)
	}
	return m, nil
}

// LocalAddrs are this machine's IPv4 addresses (not loopback).
func LocalAddrs() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		as, _ := i.Addrs()
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}

// streamsFor picks the number of data connections from the latency: long
// links need many to fill (one connection's window per round trip).
func streamsFor(rtt time.Duration) int {
	switch {
	case rtt < 2*time.Millisecond:
		return 16
	case rtt < 20*time.Millisecond:
		return 32
	default:
		return 64
	}
}

func tick(stop <-chan struct{}, f func()) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			f()
			return
		case <-t.C:
			f()
		}
	}
}

// Progress reports bytes done of total; Info says how the copy goes.
type Progress func(done, total int64)

// Client options beyond the copy's.
type ClientConfig struct {
	Opts     Options
	Compress string // auto, on, off
	Progress Progress
	Info     func(format string, a ...any)
	// Tunnel opens n SSH port forwards to the remote's port and returns the
	// local addresses (the last way when no direct connection works).
	Tunnel func(port, n int) ([]string, error)
	// NoShared: do not look for a disk both machines see (tests, where both
	// ends run on one machine).
	NoShared bool
}

// Result of a copy.
type Result struct {
	Plan    *Plan
	Bytes   int64
	Took    time.Duration
	Streams int
	Via     string // direct, reverse, tunnel, shared disk, local
}

// Client runs the local end. send: this machine sends local to the
// remote's remotePath; else it receives remotePath into local.
func Client(ctx context.Context, c *Ctrl, send bool, local, remotePath string, cfg ClientConfig) (*Result, error) {
	info := cfg.Info
	if info == nil {
		info = func(string, ...any) {}
	}
	t0 := time.Now()
	key := NewKey()
	role := "send"
	if send {
		role = "recv"
	}
	opts := cfg.Opts
	if err := c.Send(Msg{Op: "hello", Role: role, Path: remotePath, Key: key, Opts: &opts}); err != nil {
		return nil, err
	}
	ready, err := c.Recv("ready")
	if err != nil {
		return nil, err
	}
	// The remote's path is on a disk this machine sees too: copy here.
	if ready.Marker != "" && !cfg.NoShared {
		if _, err := os.Stat(ready.Marker); err == nil {
			_ = c.Send(Msg{Op: "shared"})
			src, dst := ready.Abs, local
			if send {
				src, dst = local, ready.Abs
			}
			info("both machines see %s (a shared disk): copying there directly", ready.Abs)
			p, err := Local(ctx, src, dst, opts, cfg.Progress)
			_, _ = c.Recv("bye")
			if err != nil {
				return nil, err
			}
			return &Result{Plan: p, Bytes: p.Bytes, Took: time.Since(t0), Streams: opts.Streams, Via: "shared disk"}, nil
		}
	}
	if err := c.Send(Msg{Op: "noshared"}); err != nil {
		return nil, err
	}
	conns, rtt, via, err := connect(c, key, ready, cfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, x := range conns {
			x.Close()
		}
	}()
	compress := cfg.Compress == "on" || (cfg.Compress != "off" && rtt >= 20*time.Millisecond)
	if err := c.Send(Msg{Op: "conns", N: len(conns), Compress: compress}); err != nil {
		return nil, err
	}
	info("%d data connections (%s, round trip %s)%s", len(conns), via, rtt.Round(100*time.Microsecond), map[bool]string{true: ", zstd where it helps"}[compress])
	var p *Plan
	if send {
		p, err = runSend(ctx, c, local, conns, compress, cfg.Progress)
	} else {
		p, err = runRecv(ctx, c, local, conns, opts, cfg.Progress)
	}
	if err != nil {
		return nil, err
	}
	return &Result{Plan: p, Bytes: p.Bytes, Took: time.Since(t0), Streams: len(conns), Via: via}, nil
}

// connect opens the data connections: to the remote's addresses, else the
// remote to ours, else through SSH port forwards.
func connect(c *Ctrl, key []byte, ready Msg, cfg ClientConfig) ([]*conn, time.Duration, string, error) {
	mine := map[string]bool{}
	for _, a := range LocalAddrs() {
		mine[a] = true
	}
	var addrs []string
	for _, a := range ready.Addrs {
		if !mine[a] { // a shared virtual address would reach this machine
			addrs = append(addrs, net.JoinHostPort(a, strconv.Itoa(ready.Port)))
		}
	}
	want := cfg.Opts.Streams
	force := os.Getenv("HEARTH_XFER_VIA") // tests: reverse, tunnel
	if force == "" {
		if conns, rtt, err := dialAll(addrs, key, want); err == nil {
			return conns, rtt, "direct", nil
		}
	}
	// The other way round (a machine that accepts but cannot dial out).
	l, err := net.Listen("tcp", ":0")
	if err == nil && force != "tunnel" {
		defer l.Close()
		port := l.Addr().(*net.TCPAddr).Port
		// Accept while the remote dials (its handshakes wait for ours).
		accepted := make(chan *conn, 256)
		go func() {
			for {
				x, err := Accept(l, key)
				if err != nil {
					close(accepted)
					return
				}
				accepted <- x
			}
		}()
		if err := c.Send(Msg{Op: "dialme", Addrs: LocalAddrs(), Port: port, N: want}); err != nil {
			return nil, 0, "", err
		}
		m, err := c.Recv("dialed")
		if err != nil {
			return nil, 0, "", err
		}
		if m.OK {
			var conns []*conn
			deadline := time.After(15 * time.Second)
			for len(conns) < m.N {
				select {
				case x, ok := <-accepted:
					if !ok {
						return nil, 0, "", errors.New("reverse data connections: listener closed")
					}
					conns = append(conns, x)
				case <-deadline:
					for _, y := range conns {
						y.Close()
					}
					return nil, 0, "", fmt.Errorf("reverse data connections: %d of %d arrived", len(conns), m.N)
				}
			}
			return conns, time.Duration(m.RTT) * time.Microsecond, "reverse", nil
		}
	}
	if cfg.Tunnel == nil {
		return nil, 0, "", errors.New("no direct connection between the two machines in either direction")
	}
	n := want
	if n <= 0 || n > 8 {
		n = 8
	}
	local, err := cfg.Tunnel(ready.Port, n)
	if err != nil {
		return nil, 0, "", fmt.Errorf("no direct connection, and SSH port forwarding failed: %w", err)
	}
	conns, rtt, err := dialAll(local, key, len(local))
	if err != nil {
		return nil, 0, "", fmt.Errorf("no direct connection, and through SSH: %w", err)
	}
	return conns, rtt, "through SSH", nil
}

// dialAll tries every address at once, then opens want connections (0:
// by latency) spread over the ones that answered.
func dialAll(addrs []string, key []byte, want int) ([]*conn, time.Duration, error) {
	if len(addrs) == 0 {
		return nil, 0, errors.New("no address")
	}
	type res struct {
		addr string
		c    *conn
		rtt  time.Duration
	}
	ch := make(chan res, len(addrs))
	for _, a := range addrs {
		go func(a string) {
			x, rtt, err := Dial(a, key, 2*time.Second)
			if err != nil {
				ch <- res{addr: a}
				return
			}
			ch <- res{addr: a, c: x, rtt: rtt}
		}(a)
	}
	var good []res
	for range addrs {
		if r := <-ch; r.c != nil {
			good = append(good, r)
		}
	}
	if len(good) == 0 {
		return nil, 0, errors.New("no address answered")
	}
	sort.Slice(good, func(i, j int) bool { return good[i].rtt < good[j].rtt })
	rtt := good[0].rtt
	// Keep the addresses about as close as the closest (several NICs of
	// one machine), not a far one that happens to answer too.
	var use []res
	for _, g := range good {
		if g.rtt <= rtt*3+time.Millisecond {
			use = append(use, g)
		} else {
			g.c.Close()
		}
	}
	if want <= 0 {
		want = streamsFor(rtt)
	}
	if os.Getenv("HEARTH_XFER_DEBUG") != "" {
		for _, u := range use {
			fmt.Fprintf(os.Stderr, "xfer: using %s (connect %s)\n", u.addr, u.rtt)
		}
		for _, g := range good {
			fmt.Fprintf(os.Stderr, "xfer: answered %s (connect %s)\n", g.addr, g.rtt)
		}
	}
	conns := make([]*conn, 0, want)
	for _, u := range use {
		if len(conns) < want {
			conns = append(conns, u.c)
		} else {
			u.c.Close()
		}
	}
	// The rest at once: one by one, 64 connections over a 76 ms link took
	// 13 s (connect and handshake are two round trips each).
	more := make(chan *conn, want)
	var wg sync.WaitGroup
	for i := len(conns); i < want; i++ {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			if x, _, err := Dial(a, key, 5*time.Second); err == nil {
				more <- x
			}
		}(use[i%len(use)].addr)
	}
	wg.Wait()
	close(more)
	for x := range more {
		conns = append(conns, x) // fewer than wanted: still go
	}
	return conns, rtt, nil
}

// Serve runs the remote end on the control channel (stdin/stdout of
// `hearth xfer serve`).
func Serve(ctx context.Context, c *Ctrl) error {
	hello, err := c.Recv("hello")
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = c.Send(Msg{Op: "error", Error: err.Error()})
		return err
	}
	opts := Options{}
	if hello.Opts != nil {
		opts = *hello.Opts
	}
	if len(hello.Key) != 32 {
		return fail(errors.New("bad session key"))
	}
	abs, err := filepath.Abs(expandHome(hello.Path))
	if err != nil {
		return fail(err)
	}
	if strings.HasSuffix(hello.Path, "/") && hello.Role == "recv" {
		abs += "/" // "into this directory" (Target)
	}
	if hello.Role == "send" {
		if _, err := os.Stat(abs); err != nil {
			return fail(err)
		}
	}
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return fail(err)
	}
	defer l.Close()
	marker := makeMarker(abs, hello.Role == "send")
	if marker != "" {
		defer os.Remove(marker)
	}
	if err := c.Send(Msg{Op: "ready", Addrs: append(LocalAddrs(), "127.0.0.1"), Port: l.Addr().(*net.TCPAddr).Port, Abs: abs, Marker: marker}); err != nil {
		return err
	}
	m, err := c.Recv("")
	if err != nil {
		return err
	}
	if m.Op == "shared" {
		return c.Send(Msg{Op: "bye"})
	}
	// Accept data connections until told how many there are; or dial back.
	accepted := make(chan *conn, 256)
	go func() {
		for {
			x, err := Accept(l, hello.Key)
			if err != nil {
				close(accepted)
				return
			}
			accepted <- x
		}
	}()
	var conns []*conn
	for {
		m, err = c.Recv("")
		if err != nil {
			return err
		}
		if m.Op == "dialme" {
			var addrs []string
			for _, a := range m.Addrs {
				addrs = append(addrs, net.JoinHostPort(a, strconv.Itoa(m.Port)))
			}
			cs, rtt, err := dialAll(addrs, hello.Key, m.N)
			if err != nil {
				if err := c.Send(Msg{Op: "dialed"}); err != nil {
					return err
				}
				continue
			}
			conns = cs
			if err := c.Send(Msg{Op: "dialed", OK: true, N: len(cs), RTT: rtt.Microseconds()}); err != nil {
				return err
			}
			continue
		}
		if m.Op != "conns" {
			return fail(fmt.Errorf("unexpected %q", m.Op))
		}
		break
	}
	if conns == nil {
		deadline := time.After(20 * time.Second)
		for len(conns) < m.N {
			select {
			case x, ok := <-accepted:
				if !ok {
					return fail(errors.New("data connections: listener closed"))
				}
				conns = append(conns, x)
			case <-deadline:
				return fail(fmt.Errorf("data connections: %d of %d arrived", len(conns), m.N))
			}
		}
	}
	l.Close()
	defer func() {
		for _, x := range conns {
			x.Close()
		}
	}()
	if hello.Role == "send" {
		_, err = runSend(ctx, c, abs, conns, m.Compress, nil)
	} else {
		_, err = runRecv(ctx, c, abs, conns, opts, nil)
	}
	return err
}

func expandHome(p string) string {
	if p == "~" || len(p) > 1 && p[:2] == "~/" {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[1:])
	}
	return p
}

// makeMarker puts an empty file beside path (in it, when it is a
// directory a receiver writes into); the other machine sees it when both
// mount the same disk.
func makeMarker(path string, sender bool) string {
	dir := filepath.Dir(path)
	if fi, err := os.Stat(path); err == nil && fi.IsDir() && !sender {
		dir = path
	}
	f, err := os.CreateTemp(dir, ".hearth-xfer-probe-*")
	if err != nil {
		return ""
	}
	f.Close()
	return f.Name()
}

func runSend(ctx context.Context, c *Ctrl, src string, conns []*conn, compress bool, progress Progress) (*Plan, error) {
	m, err := Scan(src)
	if err != nil {
		_ = c.Send(Msg{Op: "error", Error: err.Error()})
		return nil, err
	}
	if err := c.Send(Msg{Op: "manifest", Manifest: m}); err != nil {
		return nil, err
	}
	pm, err := c.Recv("plan")
	if err != nil {
		return nil, err
	}
	p := pm.Plan
	s, err := NewSender(src, m, p, compress)
	if err != nil {
		_ = c.Send(Msg{Op: "error", Error: err.Error()})
		return nil, err
	}
	ws := make([]deliver, len(conns))
	pipes := make([]*pipe, len(conns))
	for i, x := range conns {
		pp := newPipe(x, 3)
		pipes[i] = pp
		ws[i] = func(file int, off int64, raw int, z bool, data []byte) error {
			var fl byte
			if z {
				fl = flagZstd
			}
			return pp.send(frameChunk, fl, uint32(file), uint64(off), uint32(raw), data)
		}
	}
	stop := make(chan struct{})
	if progress != nil {
		go tick(stop, func() { progress(s.Sent(), p.Bytes) })
	}
	err = s.Run(ctx, ws)
	close(stop)
	for _, pp := range pipes {
		if err == nil {
			err = pp.send(frameEnd, 0, 0, 0, 0, nil)
		}
		if cerr := pp.close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		_ = c.Send(Msg{Op: "error", Error: "sending: " + err.Error()})
		return nil, err
	}
	if _, err := c.Recv("result"); err != nil {
		return nil, err
	}
	return p, nil
}

func runRecv(ctx context.Context, c *Ctrl, dst string, conns []*conn, opts Options, progress Progress) (*Plan, error) {
	mm, err := c.Recv("manifest")
	if err != nil {
		return nil, err
	}
	r, p, err := NewReceiver(dst, mm.Manifest, opts)
	if err != nil {
		_ = c.Send(Msg{Op: "error", Error: err.Error()})
		return nil, err
	}
	if err := c.Send(Msg{Op: "plan", Plan: p}); err != nil {
		r.Abort()
		return nil, err
	}
	stop := make(chan struct{})
	if progress != nil {
		go tick(stop, func() { progress(r.Written(), p.Bytes) })
	}
	connErr := make(chan error, len(conns))
	for _, x := range conns {
		go func(x *conn) {
			// Reading the next frame overlaps opening and writing this one.
			raws := make(chan rawFrame, 2)
			free := make(chan []byte, 3)
			for i := 0; i < 3; i++ {
				free <- nil
			}
			readErr := make(chan error, 1)
			go func() {
				defer close(raws)
				for {
					rf, err := x.readRaw(<-free)
					if err != nil {
						readErr <- err
						return
					}
					raws <- rf
				}
			}()
			dec, _ := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(ChunkSize*2))
			defer dec.Close()
			var plain []byte
			report := func(err error) {
				select {
				case <-r.Done():
					connErr <- nil
				default:
					connErr <- err
				}
			}
			for raw := range raws {
				f, err := x.open(raw)
				if err != nil {
					connErr <- err
					return
				}
				if f.typ == frameEnd {
					connErr <- nil
					return
				}
				data := f.payload
				if f.flags&flagZstd != 0 {
					plain, err = dec.DecodeAll(data, plain[:0])
					if err != nil || len(plain) != int(f.raw) {
						connErr <- errors.New("a compressed chunk does not decode")
						return
					}
					data = plain
				}
				if err := r.WriteChunk(int(f.file), int64(f.off), data); err != nil {
					connErr <- err
					return
				}
				free <- raw.buf
			}
			report(<-readErr)
		}(x)
	}
	// Errors from the sender come on the control channel.
	ctrlErr := make(chan error, 1)
	if len(p.Files) > 0 {
		go func() {
			_, err := c.Recv("never")
			ctrlErr <- err
		}()
	}
	ended := 0
	for len(p.Files) > 0 {
		select {
		case <-r.Done():
		case err := <-connErr:
			if err != nil {
				r.fail(err)
			} else if ended++; ended == len(conns) {
				select {
				case <-r.Done():
				case <-time.After(5 * time.Second):
					r.fail(errors.New("the sender finished, but chunks are missing"))
				}
			} else {
				continue
			}
		case err := <-ctrlErr:
			r.fail(err)
		case <-ctx.Done():
			r.fail(ctx.Err())
		}
		break
	}
	close(stop)
	if err := r.Err(); err != nil {
		r.Abort()
		_ = c.Send(Msg{Op: "error", Error: "receiving: " + err.Error() + " (run the same copy again to resume)"})
		return nil, err
	}
	if err := c.Send(Msg{Op: "result", Written: r.Written()}); err != nil {
		return nil, err
	}
	return p, nil
}
