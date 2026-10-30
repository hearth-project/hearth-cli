package client

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// One library sync at a time per home: the daemon and a `hearth lib pull`
// in a terminal must not merge or replace the same library together (each
// would push its own merge and the second sees a conflict). A mutex for
// this process's goroutines, an flock for other processes.

var libMu sync.Mutex

func lockLibrary(home string) func() {
	libMu.Lock()
	f, err := os.OpenFile(filepath.Join(home, "library.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return libMu.Unlock
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX) // waits for the other process
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		libMu.Unlock()
	}
}
