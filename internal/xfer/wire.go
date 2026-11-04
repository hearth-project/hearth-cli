// Package xfer copies files and directories between the user's machines,
// directly: a control channel over Hearth's SSH (authentication, the plan)
// and the data over parallel TCP connections, AES-256-GCM with a key that
// only went through SSH. Large files go in chunks over every connection at
// once and are written in place; transfers resume; nothing passes through
// the Hearth server.
package xfer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/hkdf"
)

// ChunkSize is the unit of parallelism and resumption.
const ChunkSize = 4 << 20

const (
	frameChunk = 1
	frameEnd   = 2
	flagZstd   = 1
	headerLen  = 1 + 1 + 4 + 8 + 4 // type, flags, file, offset, raw length
	maxFrame   = ChunkSize + ChunkSize/8 + headerLen + 64
)

// NewKey makes a session key; it travels only over the SSH control channel.
func NewKey() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return k
}

func mac(key []byte, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

// conn is one authenticated, encrypted data connection; data flows from
// the sender to the receiver only.
type conn struct {
	c     net.Conn
	aead  cipher.AEAD
	n     uint64
	nonce [12]byte
	buf   []byte
}

func newConn(c net.Conn, key, a, b []byte) (*conn, error) {
	ck := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, key, append(append([]byte{}, a...), b...), []byte("hearth-xfer-conn")), ck); err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(ck)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	tune(c)
	return &conn{c: c, aead: g}, nil
}

// tune leaves the socket buffers to the kernel's autotuning: setting them
// caps them at net.core.rmem_max (often 256 KiB), which starves long links
// (one 76 ms connection then carries 3 MB/s instead of 30).
func tune(c net.Conn) {
	if t, ok := c.(*net.TCPConn); ok {
		_ = t.SetNoDelay(true)
	}
}

// Dial opens a data connection and proves the session key.
func Dial(addr string, key []byte, timeout time.Duration) (*conn, time.Duration, error) {
	t0 := time.Now()
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, 0, err
	}
	rtt := time.Since(t0)
	a := make([]byte, 16)
	rand.Read(a)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write(append(append([]byte{}, a...), mac(key, []byte("hello"), a)...)); err != nil {
		c.Close()
		return nil, 0, err
	}
	resp := make([]byte, 16+32)
	if _, err := io.ReadFull(c, resp); err != nil {
		c.Close()
		return nil, 0, fmt.Errorf("handshake: %w", err)
	}
	b := resp[:16]
	if !hmac.Equal(resp[16:], mac(key, []byte("ack"), a, b)) {
		c.Close()
		return nil, 0, errors.New("handshake: the other side does not hold the session key")
	}
	_ = c.SetDeadline(time.Time{})
	cc, err := newConn(c, key, a, b)
	return cc, rtt, err
}

// Accept takes one data connection from l and checks it holds the key;
// connections that do not are closed and skipped.
func Accept(l net.Listener, key []byte) (*conn, error) {
	for {
		c, err := l.Accept()
		if err != nil {
			return nil, err
		}
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		req := make([]byte, 16+32)
		if _, err := io.ReadFull(c, req); err != nil || !hmac.Equal(req[16:], mac(key, []byte("hello"), req[:16])) {
			c.Close()
			continue
		}
		a, b := req[:16], make([]byte, 16)
		rand.Read(b)
		if _, err := c.Write(append(append([]byte{}, b...), mac(key, []byte("ack"), a, b)...)); err != nil {
			c.Close()
			continue
		}
		_ = c.SetDeadline(time.Time{})
		return newConn(c, key, a, b)
	}
}

// sealFrame builds one frame (length, header, payload, tag) in buf, sealed
// in place; frames must be written in the order they are sealed.
func (c *conn) sealFrame(buf []byte, typ, flags byte, file uint32, off uint64, raw uint32, payload []byte) []byte {
	need := headerLen + len(payload)
	total := 4 + need + c.aead.Overhead()
	if cap(buf) < total {
		buf = make([]byte, total)
	}
	b := buf[:total]
	p := b[4 : 4+need]
	p[0], p[1] = typ, flags
	binary.BigEndian.PutUint32(p[2:], file)
	binary.BigEndian.PutUint64(p[6:], off)
	binary.BigEndian.PutUint32(p[14:], raw)
	copy(p[headerLen:], payload)
	binary.BigEndian.PutUint64(c.nonce[4:], c.n)
	c.n++
	sealed := c.aead.Seal(p[:0], c.nonce[:], p, nil)
	binary.BigEndian.PutUint32(b, uint32(len(sealed)))
	return b[:4+len(sealed)]
}

// writeFrame seals and sends one frame.
func (c *conn) writeFrame(typ, flags byte, file uint32, off uint64, raw uint32, payload []byte) error {
	c.buf = c.sealFrame(c.buf, typ, flags, file, off, raw, payload)
	_, err := c.c.Write(c.buf)
	return err
}

// pipe sends frames from a goroutine, so sealing the next chunk overlaps
// sending this one; send blocks while depth frames are in flight.
type pipe struct {
	c    *conn
	free chan []byte
	out  chan []byte
	err  chan error
	werr error
}

func newPipe(c *conn, depth int) *pipe {
	p := &pipe{c: c, free: make(chan []byte, depth), out: make(chan []byte, depth), err: make(chan error, 1)}
	for i := 0; i < depth; i++ {
		p.free <- nil
	}
	go func() {
		var err error
		for b := range p.out {
			if err == nil {
				_, err = c.c.Write(b)
			}
			p.free <- b
		}
		p.err <- err
	}()
	return p
}

func (p *pipe) send(typ, flags byte, file uint32, off uint64, raw uint32, payload []byte) error {
	b := <-p.free
	select {
	case err := <-p.err:
		p.werr = err
		return err
	default:
	}
	p.out <- p.c.sealFrame(b, typ, flags, file, off, raw, payload)
	return nil
}

// close waits for every frame to be written.
func (p *pipe) close() error {
	close(p.out)
	if p.werr != nil {
		return p.werr
	}
	return <-p.err
}

// rawFrame is a sealed frame read off the connection, with its counter.
type rawFrame struct {
	n   uint64
	buf []byte
}

// readRaw reads one sealed frame without opening it (the reader goroutine);
// open (in the worker) checks and decrypts it.
func (c *conn) readRaw(buf []byte) (rawFrame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c.c, hdr[:]); err != nil {
		return rawFrame{buf: buf}, err
	}
	n := int(binary.BigEndian.Uint32(hdr[:]))
	if n > maxFrame+c.aead.Overhead() || n < headerLen+c.aead.Overhead() {
		return rawFrame{buf: buf}, errors.New("data connection: bad frame size")
	}
	if cap(buf) < n {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	if _, err := io.ReadFull(c.c, buf); err != nil {
		return rawFrame{buf: buf}, err
	}
	r := rawFrame{n: c.n, buf: buf}
	c.n++
	return r, nil
}

func (c *conn) open(r rawFrame) (frame, error) {
	var nonce [12]byte
	binary.BigEndian.PutUint64(nonce[4:], r.n)
	p, err := c.aead.Open(r.buf[:0], nonce[:], r.buf, nil)
	if err != nil {
		return frame{}, errors.New("data connection: a frame failed authentication")
	}
	return frame{typ: p[0], flags: p[1], file: binary.BigEndian.Uint32(p[2:]), off: binary.BigEndian.Uint64(p[6:]),
		raw: binary.BigEndian.Uint32(p[14:]), payload: p[headerLen:]}, nil
}

type frame struct {
	typ, flags byte
	file       uint32
	off        uint64
	raw        uint32
	payload    []byte
}

// readFrame reads one frame; buf is reused.
func (c *conn) readFrame(buf []byte) (frame, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c.c, hdr[:]); err != nil {
		return frame{}, buf, err
	}
	n := int(binary.BigEndian.Uint32(hdr[:]))
	if n > maxFrame+c.aead.Overhead() || n < headerLen+c.aead.Overhead() {
		return frame{}, buf, errors.New("data connection: bad frame size")
	}
	if cap(buf) < n {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	if _, err := io.ReadFull(c.c, buf); err != nil {
		return frame{}, buf, err
	}
	binary.BigEndian.PutUint64(c.nonce[4:], c.n)
	c.n++
	p, err := c.aead.Open(buf[:0], c.nonce[:], buf, nil)
	if err != nil {
		return frame{}, buf, errors.New("data connection: a frame failed authentication")
	}
	f := frame{typ: p[0], flags: p[1], file: binary.BigEndian.Uint32(p[2:]), off: binary.BigEndian.Uint64(p[6:]),
		raw: binary.BigEndian.Uint32(p[14:]), payload: p[headerLen:]}
	return f, buf, nil
}

func (c *conn) Close() error { return c.c.Close() }
