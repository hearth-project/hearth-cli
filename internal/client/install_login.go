package client

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// ErrInstallToken: the one-time install command was used already, has
// expired, or belongs to another account.
var ErrInstallToken = errors.New("this install command was used already or has expired (30 minutes); make a new one in the Hearth app (Machines) or with `hearth machine install-command`")

// ParseInstallToken splits "<id>.<secret>" from a console install command.
func ParseInstallToken(tok string) (id string, secret []byte, err error) {
	id, s, ok := strings.Cut(strings.TrimSpace(tok), ".")
	if ok {
		secret, err = base64.RawURLEncoding.DecodeString(s)
	}
	if !ok || err != nil || len(id) != 16 || len(secret) != crypto.KeySize {
		return "", nil, errors.New("not a valid install token (expected <id>.<secret> from the Hearth app or `hearth machine install-command`)")
	}
	return id, secret, nil
}

// LoginWithInstallToken logs this machine in with a one-time install
// command made by the Hearth app: no password, the vault key comes wrapped
// with a key only the command holds.
func LoginWithInstallToken(home, server, email, token, device string) (*Config, error) {
	id, secret, err := ParseInstallToken(token)
	if err != nil {
		return nil, err
	}
	if err := adoptCA(home); err != nil {
		return nil, err
	}
	hc, err := HTTPClient(home)
	if err != nil {
		return nil, err
	}
	resp, err := NewAPI(server, "", hc).InstallLogin(api.InstallLoginRequest{
		Email: email, ID: id, Auth: crypto.InstallAuth(secret), DeviceName: device,
	})
	if err != nil {
		if StatusOf(err) == 401 {
			return nil, ErrInstallToken
		}
		return nil, err
	}
	vk, err := crypto.UnwrapWithInstall(resp.EncVaultKeyInstall, crypto.InstallKey(secret))
	if err != nil {
		return nil, fmt.Errorf("the install command does not decrypt the vault: %w", err)
	}
	if err := saveVaultKey(home, vk); err != nil {
		return nil, err
	}
	cfg := &Config{Server: strings.TrimRight(server, "/"), Email: email, DeviceID: resp.DeviceID,
		DeviceName: device, Token: resp.Token, ExpiresAt: resp.ExpiresAt}
	return cfg, SaveConfig(home, cfg)
}

func (a *API) InstallLogin(req api.InstallLoginRequest) (api.InstallLoginResponse, error) {
	var r api.InstallLoginResponse
	_, err := a.do("POST", "/v1/auth/install/login", req, nil, nil, &r)
	return r, err
}

// ReuseInstallToken verifies an existing device login and consumes the
// install command without creating a device. Trust the caller's current
// home, not CA/proxy settings found by workspace discovery.
func ReuseInstallToken(trustHome string, cfg *Config, token string) error {
	id, secret, err := ParseInstallToken(token)
	if err != nil {
		return err
	}
	hc, err := HTTPClient(trustHome)
	if err != nil {
		return err
	}
	// Neither device bearer nor one-time proof should follow redirects.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var ack api.InstallReuseResponse
	_, err = NewAPI(cfg.Server, cfg.Token, hc).do("POST", "/v1/auth/install/reuse", api.InstallReuseRequest{
		Email: cfg.Email, ID: id, Auth: crypto.InstallAuth(secret), DeviceID: cfg.DeviceID,
	}, nil, nil, &ack)
	switch StatusOf(err) {
	case http.StatusForbidden:
		return ErrInstallToken
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return errors.New("this server does not support reusing an install command; upgrade the server, or run hearth setup in the existing workspace without --token")
	}
	if err == nil && (ack.DeviceID == "" || ack.DeviceID != cfg.DeviceID) {
		return errors.New("server did not confirm the existing device login")
	}
	return err
}
