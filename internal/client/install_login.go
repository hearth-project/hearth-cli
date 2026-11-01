package client

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// ErrInstallToken: the one-time install command was used already, has
// expired, or belongs to another account.
var ErrInstallToken = errors.New("this install command was used already or has expired (30 minutes); make a new one in the web console (Machines)")

// ParseInstallToken splits "<id>.<secret>" from a console install command.
func ParseInstallToken(tok string) (id string, secret []byte, err error) {
	id, s, ok := strings.Cut(strings.TrimSpace(tok), ".")
	if ok {
		secret, err = base64.RawURLEncoding.DecodeString(s)
	}
	if !ok || err != nil || len(id) != 16 || len(secret) != crypto.KeySize {
		return "", nil, errors.New("not a valid install token (expected <id>.<secret> from the web console)")
	}
	return id, secret, nil
}

// LoginWithInstallToken logs this machine in with a one-time install
// command from the web console: no password, the vault key comes wrapped
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
