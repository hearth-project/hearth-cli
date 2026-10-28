package library

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Limits for bundles received from the server.
const (
	maxBundleFiles = 20000
	maxBundleBytes = 256 << 20 // uncompressed
)

// Bundle packs the library into a deterministic tar.gz. The returned hash
// covers the uncompressed tar, so it changes only when content changes.
func (l Library) Bundle() (data []byte, sum string, err error) {
	type file struct {
		rel, base, src string
		mode           int64
		dir            bool
	}
	// A broken config must not silently sync the wrong directory.
	if _, err := l.LoadConfig(); err != nil {
		return nil, "", err
	}
	// Skills may live outside the library (config.toml skills.dir). They
	// travel in the bundle under skills/ either way, so the server format
	// and older clients keep working.
	skills := l.SkillsDir()
	outside := filepath.Clean(skills) != filepath.Clean(filepath.Join(l.Root, "skills"))
	var files []file
	err = filepath.WalkDir(l.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == l.Root {
				return filepath.SkipDir
			}
			return err
		}
		rel, _ := filepath.Rel(l.Root, p)
		if rel == "." {
			return nil
		}
		if outside && filepath.ToSlash(rel) == "skills" && d.IsDir() {
			return filepath.SkipDir
		}
		if strings.HasPrefix(d.Name(), ".") { // .git, .DS_Store, editor files
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case d.IsDir():
			files = append(files, file{rel: filepath.ToSlash(rel), base: l.Root, src: filepath.ToSlash(rel), mode: 0o755, dir: true})
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			mode := int64(0o644)
			if info.Mode()&0o111 != 0 {
				mode = 0o755
			}
			files = append(files, file{rel: filepath.ToSlash(rel), base: l.Root, src: filepath.ToSlash(rel), mode: mode})
		}
		return nil // symlinks and devices are not bundled
	})
	if err != nil {
		return nil, "", err
	}
	if outside {
		err = filepath.WalkDir(skills, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) && p == skills {
					return filepath.SkipDir
				}
				return err
			}
			rel, _ := filepath.Rel(skills, p)
			if rel == "." {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			name := path.Join("skills", filepath.ToSlash(rel))
			switch {
			case d.IsDir():
				files = append(files, file{rel: name, base: skills, src: filepath.ToSlash(rel), mode: 0o755, dir: true})
			case d.Type().IsRegular():
				info, err := d.Info()
				if err != nil {
					return err
				}
				mode := int64(0o644)
				if info.Mode()&0o111 != 0 {
					mode = 0o755
				}
				files = append(files, file{rel: name, base: skills, src: filepath.ToSlash(rel), mode: mode})
			}
			return nil
		})
		if err != nil {
			return nil, "", err
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, f := range files {
		h := &tar.Header{Name: f.rel, Mode: f.mode, Format: tar.FormatPAX}
		if f.dir {
			h.Typeflag, h.Name = tar.TypeDir, f.rel+"/"
			if err := tw.WriteHeader(h); err != nil {
				return nil, "", err
			}
			continue
		}
		b, err := os.ReadFile(filepath.Join(f.base, filepath.FromSlash(f.src)))
		if err != nil {
			return nil, "", err
		}
		h.Typeflag, h.Size = tar.TypeReg, int64(len(b))
		if err := tw.WriteHeader(h); err != nil {
			return nil, "", err
		}
		if _, err := tw.Write(b); err != nil {
			return nil, "", err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	h := sha256.Sum256(raw.Bytes())
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return gz.Bytes(), hex.EncodeToString(h[:]), nil
}

// Extract unpacks a bundle into dir (which must not exist yet) and returns
// the content hash. Only regular files and directories with clean relative
// paths are accepted.
func Extract(data []byte, dir string) (string, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	raw, err := io.ReadAll(io.LimitReader(zr, maxBundleBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxBundleBytes {
		return "", errors.New("bundle too large")
	}
	h := sha256.Sum256(raw)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	for n := 0; ; n++ {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if n > maxBundleFiles {
			return "", errors.New("bundle has too many entries")
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
			return "", fmt.Errorf("unsafe path in bundle: %q", hdr.Name)
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return "", err
			}
			if err := f.Close(); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("unsupported entry type in bundle: %q", hdr.Name)
		}
	}
	return hex.EncodeToString(h[:]), nil
}
