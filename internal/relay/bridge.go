// Package relay holds what the two ends of the Hearth relay share: how a
// connection between two machines is joined through the server.
//
// The relay lets a machine that nobody can connect to (a container on a
// private network, a host without an open port) accept SSH connections
// anyway: its daemon keeps one outbound WebSocket to the Hearth server, and
// when another of the user's machines asks for it, the daemon opens a second
// outbound WebSocket for that one connection; the server joins the two and
// copies bytes. The bytes are an SSH connection, so the server relays
// ciphertext it cannot read or alter without the client noticing (host keys
// are checked against the ones the machines publish end-to-end encrypted).
package relay

import "io"

// Bridge copies both ways between a and b until either direction ends, then
// closes both and returns (the copy in the other direction may still be
// unwinding: it ends as soon as the close reaches it). SSH ends a connection
// from both sides at once, so closing at the first end loses nothing:
// everything read so far has been written when a copy loop reaches EOF.
func Bridge(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	cp := func(dst, src io.ReadWriteCloser) {
		io.Copy(dst, src)
		done <- struct{}{}
	}
	go cp(b, a)
	go cp(a, b)
	<-done
	a.Close()
	b.Close()
}
