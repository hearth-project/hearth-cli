package client

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// InstallCommand is a command to run on a new machine (see
// Engine.MakeInstallCommand).
type InstallCommand struct {
	Command   string `json:"command"`
	OneTime   bool   `json:"one_time"`             // logs in without the master password
	ExpiresAt int64  `json:"expires_at,omitempty"` // unix seconds, one-time commands only
}

// MakeInstallCommand makes the "add a machine" command the web console
// makes: a one-time install command (works once, 30 minutes) whose token
// logs the new machine in without the master password. The vault key is
// wrapped here for a random secret, as the console does in the browser;
// the server sees only the ciphertext and a hash. It signs in to the
// console API with a sign-in link (POST /v1/web/link), so no server change
// is needed. When the server has no one-time commands, the plain command
// (which asks for the password on the new machine) comes back.
func (e *Engine) MakeInstallCommand() (*InstallCommand, error) {
	if e.Vault == nil {
		return nil, errors.New("no vault key on this machine; log in first")
	}
	path, err := e.API.AdminLink()
	if err != nil {
		return nil, err
	}
	cookie, err := e.consoleSession(path)
	if err != nil {
		return nil, err
	}
	web := NewAPI(e.API.Base, "", e.API.HC)
	hdr := map[string]string{"Cookie": cookie, "X-Hearth-Admin": "1"}
	var me struct {
		Install string `json:"install"`
	}
	if _, err := web.do("GET", "/v1/me", nil, nil, hdr, &me); err != nil {
		return nil, err
	}
	if me.Install == "" {
		return nil, errors.New("this server does not serve the install script (no install command); ask its admin")
	}
	secret := crypto.RandomBytes(crypto.KeySize)
	enc, err := crypto.WrapWithInstall(e.Vault.Key(), crypto.InstallKey(secret))
	if err != nil {
		return nil, err
	}
	auth := installAuthHash(crypto.InstallAuth(secret))
	var r struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		ExpiresAt int64  `json:"expires_at"`
	}
	_, err = web.do("POST", "/v1/me/install-tokens", map[string][]byte{"auth_hash": auth, "enc_vault_key": enc}, nil, hdr, &r)
	if StatusOf(err) == http.StatusNotFound || StatusOf(err) == http.StatusMethodNotAllowed {
		return &InstallCommand{Command: me.Install}, nil // an older server
	}
	if err != nil {
		return nil, err
	}
	tok := r.ID + "." + base64.RawURLEncoding.EncodeToString(secret)
	return &InstallCommand{Command: WithInstallToken(me.Install, r.Email, tok), OneTime: true, ExpiresAt: r.ExpiresAt}, nil
}

// consoleSession opens the sign-in link and returns the session cookie
// ("name=value") the server sets, without following the redirect.
func (e *Engine) consoleSession(path string) (string, error) {
	hc := *e.API.HC
	hc.Jar = nil
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Get(e.API.Base + path)
	if err != nil {
		return "", netError(e.API.Base, err)
	}
	resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "hearth_session" && c.Value != "" {
			return c.Name + "=" + c.Value, nil
		}
	}
	return "", fmt.Errorf("the server did not open a console session (%s)", resp.Status)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9@._+-]+$`)

// installAuthHash is what the server keeps to check the new machine's
// InstallAuth (internal/server/install_tokens.go).
func installAuthHash(auth []byte) []byte {
	h := sha256.Sum256(auth)
	return h[:]
}

// WithInstallToken appends the setup arguments to the install one-liner
// (curl ... | sh, or { ... } | sh with a proxy fallback), as the console's
// withInstallToken does.
func WithInstallToken(cmd, email, token string) string {
	q := email
	if !shellSafe.MatchString(email) {
		q = "'" + strings.ReplaceAll(email, "'", `'\''`) + "'"
	}
	if strings.HasSuffix(cmd, "| sh") {
		cmd = strings.TrimSuffix(cmd, "| sh") + "| sh -s --"
	}
	return cmd + " --email " + q + " --token " + token
}
