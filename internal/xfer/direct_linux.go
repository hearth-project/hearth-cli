package xfer

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// Buffered writes into one file serialise on its inode lock (about 2-3
// GB/s whatever the number of writers); O_DIRECT writes of whole chunks go
// in parallel up to what the disk takes. tmpfs and some file systems
// refuse O_DIRECT: then buffered.
func openDirect(p string) *os.File {
	f, err := os.OpenFile(p, os.O_WRONLY|syscall.O_DIRECT, 0)
	if err != nil {
		return nil
	}
	return f
}

var aligned = sync.Pool{New: func() any {
	raw := make([]byte, ChunkSize+4096)
	a := (4096 - int(uintptr(unsafe.Pointer(&raw[0]))%4096)) % 4096
	b := raw[a : a+ChunkSize]
	return &b
}}

// writeDirect writes a whole chunk at a chunk-aligned offset.
func writeDirect(f *os.File, data []byte, off int64) error {
	bp := aligned.Get().(*[]byte)
	defer aligned.Put(bp)
	copy(*bp, data)
	_, err := f.WriteAt(*bp, off)
	return err
}

// allocate reserves the file's blocks (direct writes into holes are slow
// or refused); where that is not supported, the file only gets its size.
func allocate(f *os.File, size int64) error {
	if size > 0 && syscall.Fallocate(int(f.Fd()), 0, 0, size) == nil {
		return nil
	}
	return f.Truncate(size)
}
