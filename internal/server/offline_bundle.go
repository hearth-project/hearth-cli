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

func shellArg(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Returned only through the signed-in console. The hash comes from the
// trusted machine, not from the download. The token is a positional argument
// for setup and is never part of a download URL or request.
func (d *distState) bundleCommand(proxies []string) string {
	var cases strings.Builder
	for _, name := range d.names() {
		fmt.Fprintf(&cases, "%s) sum=%s;; ", name, d.bundles[name+".tar"].digest)
	}
	const script = `set -eu
os=$(uname -s | tr A-Z a-z); arch=$(uname -m)
case "$arch" in x86_64) arch=amd64;; aarch64) arch=arm64;; esac
name=hearth-$os-$arch
case "$name" in @CASES@ *) echo "hearth: no offline bundle for $name" >&2; exit 1;; esac
if [ "$os" = linux ] && ! uname -r | awk -F. '{exit !($1>3 || ($1==3 && $2>=2))}'; then echo "hearth: Linux 3.2 or newer required (found $(uname -r))" >&2; exit 1; fi
dir=$(mktemp -d); trap 'rm -rf "$dir"' EXIT
fetch() { if command -v wget >/dev/null 2>&1; then wget --no-netrc -q -t 1 -T 10 @WGET_TLS@ -O "$dir/package.tar" "$1" && return 0; fi; command -v curl >/dev/null 2>&1 && curl -q -fsS --connect-timeout 5 @CURL_TLS@ -o "$dir/package.tar" "$1"; }
if ! fetch "$1/bootstrap/$name.tar" && ! fetch "$2/bootstrap/$name.tar"; then @PROXY_RETRY@; fi; shift 2
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum < "$dir/package.tar"); else got=$(shasum -a 256 < "$dir/package.tar"); fi
[ "${got%% *}" = "$sum" ] || { echo "hearth: bundle checksum mismatch; copy a fresh install command" >&2; exit 1; }
tar -xf "$dir/package.tar" -C "$dir" && sh "$dir/install.sh" "$@"`
	wgetTLS, curlTLS := "", ""
	if d.pin != "" {
		wgetTLS, curlTLS = "--no-check-certificate", "-k"
	}
	retry := `echo "hearth: cannot download $name.tar with wget or curl" >&2; exit 1`
	if len(proxies) != 0 {
		// These site proxy URLs stay in the authenticated command, never the tar.
		var quoted []string
		for _, proxy := range proxies {
			quoted = append(quoted, shellArg(proxy))
		}
		retry = `ok=; for p in ` + strings.Join(quoted, " ") + `; do https_proxy=$p; export https_proxy; if fetch "$1/bootstrap/$name.tar"; then ok=1; break; fi; done; [ "$ok" = 1 ] || { ` + retry + `; }`
	}
	body := strings.NewReplacer("@PROXY_RETRY@", retry, "@CASES@", cases.String(), "@WGET_TLS@", wgetTLS, "@CURL_TLS@", curlTLS).Replace(script)
	return "sh -c " + shellArg(strings.ReplaceAll(body, "\n", "; ")) + " hearth-install " + shellArg(d.PublicURL) + " " + shellArg(d.BootstrapURL)
}
