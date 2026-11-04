package xfer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Entry is one item to copy; Path is relative to the source root ("" for
// the root itself), with forward slashes.
type Entry struct {
	Path  string      `json:"p"`
	Kind  byte        `json:"k"` // 'f' file, 'd' directory, 'l' symlink
	Mode  fs.FileMode `json:"m"`
	Size  int64       `json:"s,omitempty"`
	MTime int64       `json:"t,omitempty"` // unix nanoseconds
	Link  string      `json:"l,omitempty"`
}

// Manifest is the source side: its name and every entry.
type Manifest struct {
	Name    string  `json:"name"` // base name of the source
	Dir     bool    `json:"dir"`
	Entries []Entry `json:"entries"`
	Bytes   int64   `json:"bytes"`
}

// Scan lists src (a file or a directory, symlinks inside kept as links).
func Scan(src string) (*Manifest, error) {
	src = filepath.Clean(src)
	fi, err := os.Stat(src) // the root itself is followed
	if err != nil {
		return nil, err
	}
	m := &Manifest{Name: filepath.Base(src), Dir: fi.IsDir()}
	if !fi.IsDir() {
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file or directory", src)
		}
		m.Entries = []Entry{{Kind: 'f', Mode: fi.Mode().Perm(), Size: fi.Size(), MTime: fi.ModTime().UnixNano()}}
		m.Bytes = fi.Size()
		return m, nil
	}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			rel = ""
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			m.Entries = append(m.Entries, Entry{Path: rel, Kind: 'd', Mode: info.Mode().Perm(), MTime: info.ModTime().UnixNano()})
		case info.Mode()&fs.ModeSymlink != 0:
			t, err := os.Readlink(p)
			if err != nil {
				return err
			}
			m.Entries = append(m.Entries, Entry{Path: rel, Kind: 'l', Link: t})
		case info.Mode().IsRegular():
			m.Entries = append(m.Entries, Entry{Path: rel, Kind: 'f', Mode: info.Mode().Perm(), Size: info.Size(), MTime: info.ModTime().UnixNano()})
			m.Bytes += info.Size()
		}
		return nil // sockets, devices, fifos: skipped
	})
	return m, err
}

// Target resolves where the source lands, like cp -r: into dst when dst
// is an existing directory or ends in "/", else as dst.
func Target(dst string, m *Manifest) (string, error) {
	slash := strings.HasSuffix(dst, "/")
	dst = filepath.Clean(dst)
	fi, err := os.Stat(dst)
	switch {
	case err == nil && fi.IsDir():
		return filepath.Join(dst, m.Name), nil
	case err == nil && m.Dir:
		return "", fmt.Errorf("%s exists and is not a directory", dst)
	case err == nil:
		return dst, nil // a file onto a file
	case !errors.Is(err, fs.ErrNotExist):
		return "", err
	case slash:
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return "", err
		}
		return filepath.Join(dst, m.Name), nil
	default:
		if _, err := os.Stat(filepath.Dir(dst)); err != nil {
			return "", fmt.Errorf("%s: its directory does not exist", dst)
		}
		return dst, nil
	}
}

// safeRel rejects paths that would leave the target (a manifest comes from
// the other machine).
func safeRel(p string) bool {
	if p == "" {
		return true
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "" || part == "." {
			return false
		}
	}
	return true
}
