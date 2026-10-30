package server

import (
	"github.com/hearth-project/hearth-cli/skills"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Distribution lets the server hand out client binaries and a one-line
// installer, so a new machine needs nothing but curl.
type Distribution struct {
	PublicURL  string // e.g. https://hearth.example.com:8787 (what clients use)
	BinaryPath string // client binary for the server's own platform (usually os.Executable())
	// Dir optionally holds binaries for other platforms, named
	// hearth-<os>-<arch> (scripts/build-dist.sh); they override BinaryPath.
	Dir     string
	CertPEM []byte // set when the server uses a self-signed certificate
	// PinInstall adds --pinnedpubkey to the one-liner for a self-signed
	// server. Off by default: plain -k downloads the installer, which then
	// pins the embedded certificate for every later connection (trust on
	// first use), the usual trade-off inside a private network.
	PinInstall bool
	// NoAutoUpdate tells client daemons not to install new builds by
	// themselves (they still can with `hearth self-update`).
	NoAutoUpdate bool
	// Version of the served builds: <Dir>/VERSION (scripts/build-dist.sh),
	// else the server's own version.
	Version string
}

// distBinary is one downloadable client build.
type distBinary struct {
	path, sha256 string
}

type distState struct {
	Distribution
	bins map[string]distBinary // hearth-<os>-<arch>
	pin  string
}

// distNameRE limits served names to the platforms the client supports.
var distNameRE = regexp.MustCompile(`^hearth-(linux|darwin)-(amd64|arm64)$`)

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// names returns the available binaries, sorted.
func (d *distState) names() []string {
	out := make([]string, 0, len(d.bins))
	for n := range d.bins {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func newDist(d *Distribution) (*distState, error) {
	if d == nil || d.PublicURL == "" || d.BinaryPath == "" {
		return nil, nil
	}
	if strings.ContainsAny(d.PublicURL, "'\"`$\\ \n") {
		return nil, fmt.Errorf("invalid public URL %q", d.PublicURL)
	}
	ds := &distState{Distribution: *d, bins: map[string]distBinary{}}
	own := fmt.Sprintf("hearth-%s-%s", runtime.GOOS, runtime.GOARCH)
	sum, err := fileSHA256(d.BinaryPath)
	if err != nil {
		return nil, err
	}
	ds.bins[own] = distBinary{d.BinaryPath, sum}
	if d.Dir != "" {
		entries, err := os.ReadDir(d.Dir)
		if err != nil {
			return nil, fmt.Errorf("dist dir: %w", err)
		}
		for _, e := range entries {
			if !distNameRE.MatchString(e.Name()) || !e.Type().IsRegular() {
				continue
			}
			p := filepath.Join(d.Dir, e.Name())
			sum, err := fileSHA256(p)
			if err != nil {
				return nil, err
			}
			ds.bins[e.Name()] = distBinary{p, sum}
		}
	}
	if d.Dir != "" {
		if b, err := os.ReadFile(filepath.Join(d.Dir, "VERSION")); err == nil {
			ds.Version = strings.TrimSpace(string(b))
		}
	}
	ds.PublicURL = strings.TrimRight(ds.PublicURL, "/")
	if len(d.CertPEM) > 0 {
		if ds.pin, err = SPKIPin(d.CertPEM); err != nil {
			return nil, err
		}
	}
	return ds, nil
}

// siteProxies are the proxies the organisation lists in the site defaults
// (<site>/net.json {"proxies": [...]}), for machines that reach the server
// only through one.
func (s *Server) siteProxies() []string {
	var n struct {
		Proxies []string `json:"proxies"`
	}
	if s.cfg.SiteDir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(s.cfg.SiteDir, "net.json"))
	if err != nil || json.Unmarshal(b, &n) != nil {
		return nil
	}
	var out []string
	for _, p := range n.Proxies {
		if u, err := url.Parse(p); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && !strings.ContainsAny(p, "'\" \\$`;|&") {
			out = append(out, p)
		}
	}
	return out
}

// InstallCommand is the one-liner to run on a new machine. With site
// proxies it falls back to the first one when the server does not answer
// directly within a few seconds.
func (s *Server) InstallCommand() string {
	cmd := s.installCommand()
	if ps := s.siteProxies(); cmd != "" && len(ps) > 0 {
		get, _, _ := strings.Cut(cmd, " | sh")
		retry := strings.Replace(get, "curl -fsSL", "curl -fsSL -x "+ps[0], 1)
		get = strings.Replace(get, "curl -fsSL", "curl -fsSL --connect-timeout 5", 1)
		return "{ " + get + " || " + retry + "; } | sh"
	}
	return cmd
}

func (s *Server) installCommand() string {
	d := s.dist
	if d == nil {
		return ""
	}
	if d.pin != "" && !d.PinInstall {
		// Self-signed: -k for the first download only; the installer then
		// trusts exactly the embedded certificate.
		return fmt.Sprintf("curl -fsSL -k %s/install.sh | sh", d.PublicURL)
	}
	if d.pin != "" {
		// -k only skips CA verification; --pinnedpubkey still aborts unless
		// the server presents exactly this key.
		return fmt.Sprintf("curl -fsSL -k --pinnedpubkey '%s' %s/install.sh | sh", d.pin, d.PublicURL)
	}
	return fmt.Sprintf("curl -fsSL %s/install.sh | sh", d.PublicURL)
}

const installTemplate = `#!/bin/sh
# hearth installer generated by @URL@
set -eu
URL='@URL@'

os=$(uname -s | tr 'A-Z' 'a-z')
arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
BIN="hearth-$os-$arch"
case "$BIN" in
@CASES@
  *) echo "hearth: no build for $os-$arch on this server (available: @NAMES@)" >&2; exit 1 ;;
esac

BIN_DIR="${HEARTH_BIN_DIR:-$HOME/.local/bin}"
AS_HOME="${HEARTH_HOME:-$HOME/.hearth}"
mkdir -p "$BIN_DIR" "$AS_HOME"
chmod 700 "$AS_HOME"
CURL="curl -fsSL"
@CA_BLOCK@
# No direct route to the server (a proxy missing in this shell): try the
# organisation's proxies; the first that reaches it is used from here on.
PROXIES='@PROXIES@'
if [ -n "$PROXIES" ] && [ -z "${https_proxy:-}${HTTPS_PROXY:-}" ] && ! $CURL --connect-timeout 5 -o /dev/null "$URL/healthz" 2>/dev/null; then
  for p in $PROXIES; do
    if $CURL --connect-timeout 5 -x "$p" -o /dev/null "$URL/healthz" 2>/dev/null; then
      https_proxy="$p"; http_proxy="$p"; no_proxy="${no_proxy:+$no_proxy,}127.0.0.1,localhost,::1"
      export https_proxy http_proxy no_proxy
      echo "network: the server is not reachable directly; using proxy $p"
      break
    fi
  done
fi
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
$CURL -o "$tmp" "$URL/download/$BIN"
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp" | cut -d' ' -f1); else got=$(shasum -a 256 "$tmp" | cut -d' ' -f1); fi
if [ "$got" != "$SHA256" ]; then
  echo "hearth: checksum mismatch for downloaded binary" >&2
  exit 1
fi
chmod 755 "$tmp"
mv "$tmp" "$BIN_DIR/hearth"
trap - EXIT
echo "hearth $("$BIN_DIR/hearth" --version 2>/dev/null | sed 's/^hearth version //') installed to $BIN_DIR/hearth"

# Make "hearth" available in new terminals: a link in a directory already on
# PATH when possible (works at once, also for non-interactive shells), and
# a PATH line in the shell startup files. HEARTH_NO_PATH=1 skips both.
if [ "${HEARTH_NO_PATH:-}" != "1" ]; then
  linked=""
  case ":$PATH:" in
    *":$BIN_DIR:"*) linked="$BIN_DIR" ;;
    *)
      for d in ${HEARTH_LINK_DIRS:-/usr/local/bin /opt/homebrew/bin "$HOME/bin"}; do
        case ":$PATH:" in *":$d:"*) ;; *) continue ;; esac
        if [ -d "$d" ] && [ -w "$d" ] && { [ ! -e "$d/hearth" ] || [ -L "$d/hearth" ]; }; then
          ln -sf "$BIN_DIR/hearth" "$d/hearth" && linked="$d" && break
        fi
      done ;;
  esac
  line="export PATH=\"$BIN_DIR:\$PATH\"  # added by the hearth installer"
  for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile" "$HOME/.bash_profile"; do
    case "$rc" in
      */.bashrc|*/.profile) ;;          # create these if missing
      *) [ -f "$rc" ] || continue ;;     # only extend existing zsh / bash_profile
    esac
    if ! grep -qsF "added by the hearth installer" "$rc"; then
      printf '\n%s\n' "$line" >> "$rc"
    fi
  done
fi

# Organisation defaults published on this server (recipes, files, login-token
# locations), fetched before login so they apply from the first step.
"$BIN_DIR/hearth" site pull --server "$URL" --quiet || true

# The hearth skill for every coding agent on this
# machine, so the user can just ask their agent. HEARTH_NO_SKILLS=1 skips.
if [ "${HEARTH_NO_SKILLS:-}" != "1" ]; then
  "$BIN_DIR/hearth" onboard install-skill --quiet || true
fi

# --install-only: just install (an agent running the hearth skill
# drives the rest and has the user log in from their own terminal).
if [ "${1:-}" = "--install-only" ]; then
  [ -n "${linked:-}" ] || echo "note: open a new terminal, or run now:  export PATH=\"$BIN_DIR:\$PATH\""
  echo "next: hearth setup --server $URL   (account from this machine's identity; --email to choose)"
  echo "then: on any other machine with Hearth, tell its agent \"恢复 <this machine> 的 Codex 环境\" to set up this machine's software"
  exit 0
fi

# stdin is this script when piped from curl; read the password from the tty.
if [ -r /dev/tty ] && [ -z "${HEARTH_PASSWORD:-}" ]; then
  exec "$BIN_DIR/hearth" setup --server "$URL" "$@" </dev/tty
fi
exec "$BIN_DIR/hearth" setup --server "$URL" "$@"
`

func (s *Server) handleInstall(w http.ResponseWriter, _ *http.Request) {
	d := s.dist
	if d == nil {
		writeErr(w, http.StatusNotFound, "installer not enabled (start the server with --public-url)")
		return
	}
	caBlock := ""
	if len(d.CertPEM) > 0 {
		caBlock = "umask 077\ncat > \"$AS_HOME/ca.pem\" <<'HEARTH_CA'\n" + strings.TrimSpace(string(d.CertPEM)) +
			"\nHEARTH_CA\numask 022\nCURL=\"$CURL --cacert $AS_HOME/ca.pem\""
	}
	var cases strings.Builder
	for _, n := range d.names() {
		fmt.Fprintf(&cases, "  %s) SHA256='%s' ;;\n", n, d.bins[n].sha256)
	}
	script := strings.NewReplacer("@URL@", d.PublicURL, "@CASES@", strings.TrimRight(cases.String(), "\n"),
		"@NAMES@", strings.Join(d.names(), " "), "@CA_BLOCK@", caBlock, "@PROXIES@", strings.Join(s.siteProxies(), " ")).Replace(installTemplate)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	io.WriteString(w, script)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	d := s.dist
	var b distBinary
	ok := false
	if d != nil {
		b, ok = d.bins[r.PathValue("name")]
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "no such binary")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Hearth-Sha256", b.sha256)
	http.ServeFile(w, r, b.path)
}

// handleDistManifest lists the binaries and their checksums, for
// `hearth self-update`. Public, like /install.sh.
// handleOnboardSkill serves the hearth agent skill, so an agent on a
// machine without Hearth can be pointed at <server>/onboard/SKILL.md.
func (s *Server) handleOnboardSkill(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	body := skills.Hearth
	if s.dist != nil {
		cmd := s.InstallCommand()
		note := "\n> This server: " + s.dist.PublicURL + ". Install command (step A):\n> `" +
			strings.Replace(cmd, "| sh", "| sh -s -- --install-only", 1) + "`\n"
		if i := strings.Index(body, "\n# Hearth\n"); i >= 0 {
			body = body[:i] + note + body[i:]
		}
	}
	io.WriteString(w, body)
}

func (s *Server) handleDistManifest(w http.ResponseWriter, _ *http.Request) {
	d := s.dist
	if d == nil {
		writeErr(w, http.StatusNotFound, "distribution not enabled (start the server with --public-url)")
		return
	}
	out := map[string]string{}
	for n, b := range d.bins {
		out[n] = b.sha256
	}
	writeJSON(w, http.StatusOK, map[string]any{"binaries": out, "version": d.Version, "auto_update": s.Settings().ClientAutoUpdate})
}

// DistPlatforms lists the client builds served by /install.sh.
func (s *Server) DistPlatforms() []string {
	if s.dist == nil {
		return nil
	}
	out := s.dist.names()
	for i, n := range out {
		out[i] = strings.TrimPrefix(n, "hearth-")
	}
	return out
}

// handleInfo tells clients (and the setup skill) how to sign up. Public.
func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"registration": s.registration()})
}
