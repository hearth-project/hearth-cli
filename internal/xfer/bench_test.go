package xfer

import (
	"net"
	"testing"
)

// The data path of one connection: seal, send, open (loopback).
func BenchmarkConn(b *testing.B) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	key := NewKey()
	got := make(chan *conn, 1)
	go func() { c, _ := Accept(l, key); got <- c }()
	s, _, err := Dial(l.Addr().String(), key, 0)
	if err != nil {
		b.Fatal(err)
	}
	r := <-got
	data := make([]byte, ChunkSize)
	done := make(chan struct{})
	go func() {
		var buf []byte
		for i := 0; i < b.N; i++ {
			_, buf, _ = r.readFrame(buf)
		}
		close(done)
	}()
	b.SetBytes(ChunkSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.writeFrame(frameChunk, 0, 0, 0, ChunkSize, data)
	}
	<-done
}
