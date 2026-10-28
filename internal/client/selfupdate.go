package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// PlatformBinary is this client's name in the server's distribution.
func PlatformBinary() string { return "hearth-" + runtime.GOOS + "-" + runtime.GOARCH }

// UpdateResult describes a self-update.
type UpdateResult struct {
	Path    string
	Updated bool
	SHA256  string
	Version string // version of the server's builds ("" if it does not say)
	// Disabled: the server asked daemons not to update by themselves.
	Disabled bool
}

// UpdateOptions tunes SelfUpdateWith.
type UpdateOptions struct {
	// Auto is a daemon's periodic check: it honours the server's
	// auto_update switch and this machine's opt-out.
	Auto bool
}

// SelfUpdate replaces the binary at exe with the server's build for this
// platform when their checksums differ. The download is verified against
// the manifest and swapped in atomically (rename in the same directory).
func SelfUpdate(home, server, exe string) (UpdateResult, error) {
	return SelfUpdateWith(home, server, exe, UpdateOptions{})
}

// SelfUpdateWith is SelfUpdate with options.
func SelfUpdateWith(home, server, exe string, o UpdateOptions) (UpdateResult, error) {
	res := UpdateResult{Path: exe}
	if o.Auto && !AutoUpdateEnabled(home) {
		res.Disabled = true
		return res, nil
	}
	hc, err := HTTPClient(home)
	if err != nil {
		return res, err
	}
	hc.Timeout = 10 * time.Minute
	base := NewAPI(server, "", hc).Base
	resp, err := hc.Get(base + "/v1/dist")
	if err != nil {
		return res, err
	}
	var m struct {
		Binaries   map[string]string `json:"binaries"`
		Version    string            `json:"version"`
		AutoUpdate *bool             `json:"auto_update"`
	}
	err = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || err != nil {
		return res, fmt.Errorf("server has no client distribution (status %d); it must run with --public-url", resp.StatusCode)
	}
	res.Version = m.Version
	if o.Auto && m.AutoUpdate != nil && !*m.AutoUpdate {
		res.Disabled = true
		return res, nil
	}
	want, ok := m.Binaries[PlatformBinary()]
	if !ok {
		return res, fmt.Errorf("server has no build for %s", PlatformBinary())
	}
	res.SHA256 = want
	if cur, err := fileSum(exe); err == nil && cur == want {
		return res, nil
	}
	resp, err = hc.Get(base + "/download/" + PlatformBinary())
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("download: status %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".hearth-update-*")
	if err != nil {
		return res, err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return res, err
	}
	if err := tmp.Close(); err != nil {
		return res, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return res, fmt.Errorf("checksum mismatch: got %s, manifest says %s", got, want)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return res, err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		return res, err
	}
	res.Updated = true
	return res, nil
}

func fileSum(p string) (string, error) {
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

// Registration returns how a server accepts new accounts: invite, open or
// closed ("" when the server does not say).
func Registration(home, server string) string {
	hc, err := HTTPClient(home)
	if err != nil {
		return ""
	}
	hc.Timeout = 5 * time.Second
	resp, err := hc.Get(NewAPI(server, "", hc).Base + "/v1/info")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var v struct {
		Registration string `json:"registration"`
	}
	json.NewDecoder(resp.Body).Decode(&v)
	return v.Registration
}

// autoUpdatePath: this machine's opt-out of daemon self-updates (not synced).
func autoUpdatePath(home string) string { return filepath.Join(home, "auto-update.json") }

// AutoUpdateEnabled reports whether the daemon installs new builds by
// itself on this machine (default yes; HEARTH_AUTO_UPDATE=0 turns it off).
func AutoUpdateEnabled(home string) bool {
	if os.Getenv("HEARTH_AUTO_UPDATE") == "0" {
		return false
	}
	var f struct {
		Enabled *bool `json:"enabled"`
	}
	if err := loadJSON(autoUpdatePath(home), &f); err == nil && f.Enabled != nil {
		return *f.Enabled
	}
	return true
}

// SetAutoUpdate turns daemon self-updates on or off for this machine.
func SetAutoUpdate(home string, on bool) error {
	b, err := jsonIndent(map[string]bool{"enabled": on})
	if err != nil {
		return err
	}
	return writeFileAtomic(autoUpdatePath(home), b, 0o600)
}

// ServerBuild returns the client version the server hands out ("" when
// unknown or unreachable; quick, for `hearth status`).
func ServerBuild(home, server string) string {
	hc, err := HTTPClient(home)
	if err != nil {
		return ""
	}
	hc.Timeout = 3 * time.Second
	resp, err := hc.Get(NewAPI(server, "", hc).Base + "/v1/dist")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var m struct {
		Version string `json:"version"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&m) != nil {
		return ""
	}
	return m.Version
}
