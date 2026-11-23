package server

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// A deterministic tar: hash it once at startup, then stream it from the same
// distribution files. No second copy of every binary in RAM or on disk.
type offlineBundle struct {
	binary, installer, digest string
}

func (b offlineBundle) write(w io.Writer) error {
	f, err := os.Open(b.binary)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	for _, member := range []struct {
		name string
		size int64
		src  io.Reader
	}{
		{"install.sh", int64(len(b.installer)), strings.NewReader(b.installer)},
		{"hearth", st.Size(), f},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: member.name, Mode: 0755, Size: member.size, Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		if _, err := io.CopyN(tw, member.src, member.size); err != nil {
			return err
		}
	}
	return tw.Close()
}

func (d *distState) prepareBundles() error {
	api, err := url.Parse(d.PublicURL)
	if err != nil || api.Scheme != "https" || api.Hostname() == "" || api.User != nil || api.RawQuery != "" || api.Fragment != "" {
		return fmt.Errorf("offline bootstrap requires an HTTPS public URL")
	}
	mirror, err := url.Parse(d.BootstrapURL)
	if err != nil || mirror.Scheme != "http" || mirror.Hostname() == "" || mirror.User != nil || mirror.RawQuery != "" || mirror.Fragment != "" {
		return fmt.Errorf("invalid offline bootstrap URL")
	}
	d.bundles = make(map[string]offlineBundle, len(d.bins))
	// No site configuration or credentials in the public tar. setup and site
	// pull use the main HTTPS origin and the certificate inside the installer.
	script := d.installer(nil, `cp "$(dirname "$0")/hearth" "$tmp"`)
	for name, bin := range d.bins {
		b := offlineBundle{binary: bin.path, installer: script}
		h := sha256.New()
		if err := b.write(h); err != nil {
			return fmt.Errorf("offline bundle %s: %w", name, err)
		}
		b.digest = hex.EncodeToString(h.Sum(nil))
		d.bundles[name+".tar"] = b
	}
	return nil
}

// BootstrapHandler is the entire HTTP listener. Never mount the API, login,
// install script, checksum manifest or one-time command on this port.
func (s *Server) BootstrapHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /bootstrap/{name}", s.handleBundle)
	return mux
}

func (s *Server) handleBundle(w http.ResponseWriter, r *http.Request) {
	if s.dist == nil {
		http.NotFound(w, r)
		return
	}
	b, ok := s.dist.bundles[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	if r.Method != http.MethodHead {
		if err := b.write(w); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}
