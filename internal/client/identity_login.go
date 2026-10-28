package client

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// PlatformSecret returns the platform credential that proves the identity
// to a hearth server with an identity broker. Only the user the container
// was created for has it, so it must match the detected account.
func PlatformSecret(id LocalIdentity) ([]byte, bool) {
	user := strings.TrimSpace(os.Getenv("SYS_USER_NAME"))
	if user == "" {
		user = strings.TrimSpace(os.Getenv("PDC_AGENT_USER_ID"))
	}
	if user == "" || !strings.EqualFold(user, id.Username) {
		return nil, false
	}
	sk := strings.TrimSpace(os.Getenv("PDC_USER_SK"))
	if sk == "" {
		return nil, false
	}
	return []byte(sk), true
}

// EnableIdentityLogin wraps this workspace's vault key with the platform
// identity key and stores it on the server (needs a normal login).
func EnableIdentityLogin(home string, secret []byte, force bool) error {
	cfg, err := LoadConfig(home)
	if err != nil {
		return err
	}
	v, err := LoadVault(home)
	if err != nil {
		return err
	}
	key := crypto.IdentityKey(secret)
	wrapped, err := crypto.WrapWithIdentity(v.Key(), key)
	if err != nil {
		return err
	}
	hc, err := HTTPClient(home)
	if err != nil {
		return err
	}
	a := NewAPI(cfg.Server, cfg.Token, hc)
	if err := a.EnableIdentity(api.IdentityEnableRequest{EncVaultKeyIdentity: wrapped, Force: force}); err != nil {
		if StatusOf(err) == 404 {
			return errors.New("this server has no identity broker configured")
		}
		return err
	}
	return nil
}

// AutoEnableIdentityLogin uploads the passwordless unlock when the machine
// has the account's platform credential and the account has not opted out.
// It is best effort: old servers (404), shared containers with somebody
// else's credential, and accounts without the broker just keep using the
// master password.
func AutoEnableIdentityLogin(home string) error {
	cfg, err := LoadConfig(home)
	if err != nil {
		return nil
	}
	id, ok := DetectIdentity(home)
	if !ok {
		return nil
	}
	secret, ok := PlatformSecret(id)
	if !ok {
		return nil
	}
	hc, err := HTTPClient(home)
	if err != nil {
		return nil
	}
	a := NewAPI(cfg.Server, cfg.Token, hc)
	st, err := a.IdentityStatus()
	if err != nil || st.Enabled || st.Disabled {
		return nil
	}
	if err := EnableIdentityLogin(home, secret, false); err != nil {
		return err
	}
	return nil
}

// DisableIdentityLogin removes the passwordless unlock from the account.
func DisableIdentityLogin(home string) error {
	cfg, err := LoadConfig(home)
	if err != nil {
		return err
	}
	hc, err := HTTPClient(home)
	if err != nil {
		return err
	}
	return NewAPI(cfg.Server, cfg.Token, hc).DisableIdentity()
}

// IdentityServerStatus reports whether this account has a platform unlock.
func IdentityServerStatus(home string) (api.IdentityStatusResponse, error) {
	cfg, err := LoadConfig(home)
	if err != nil {
		return api.IdentityStatusResponse{}, err
	}
	hc, err := HTTPClient(home)
	if err != nil {
		return api.IdentityStatusResponse{}, err
	}
	return NewAPI(cfg.Server, cfg.Token, hc).IdentityStatus()
}

// LoginWithIdentity logs in without the master password by proving
// possession of the platform secret the broker holds for this account.
func LoginWithIdentity(home, server, email string, secret []byte, device string) (*Config, error) {
	if err := adoptCA(home); err != nil {
		return nil, err
	}
	hc, err := HTTPClient(home)
	if err != nil {
		return nil, err
	}
	key := crypto.IdentityKey(secret)
	ts := time.Now().Unix()
	resp, err := NewAPI(server, "", hc).IdentityLogin(api.IdentityLoginRequest{
		Email:      email,
		Timestamp:  ts,
		Proof:      crypto.IdentityProof(key, api.IdentityLoginMessage(email, ts)),
		DeviceName: device,
	})
	if err != nil {
		return nil, err
	}
	vk, err := crypto.UnwrapWithIdentity(resp.EncVaultKeyIdentity, key)
	if err != nil {
		return nil, fmt.Errorf("platform identity does not decrypt the vault: %w", err)
	}
	if err := saveVaultKey(home, vk); err != nil {
		return nil, err
	}
	cfg := &Config{Server: strings.TrimRight(server, "/"), Email: email, DeviceID: resp.DeviceID,
		DeviceName: device, Token: resp.Token, ExpiresAt: resp.ExpiresAt}
	return cfg, SaveConfig(home, cfg)
}
