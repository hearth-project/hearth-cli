//go:build !linux

package xfer

import "os"

func openDirect(string) *os.File                { return nil }
func writeDirect(*os.File, []byte, int64) error { return os.ErrInvalid }
func allocate(f *os.File, size int64) error     { return f.Truncate(size) }
