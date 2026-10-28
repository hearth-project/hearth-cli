package client

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// KDF used for new accounts; tests override it to keep Argon2id cheap.
var KDF = crypto.DefaultKDF

// CheckServerURL rejects plain HTTP except for loopback addresses, since
// tokens and auth keys travel in requests.
func CheckServerURL(raw string, allowInsecure bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid server URL %q", raw)
	}
	if u.Scheme == "https" || allowInsecure {
		return nil
	}
	h := u.Hostname()
	if u.Scheme == "http" && (h == "localhost" || h == "127.0.0.1" || h == "::1") {
		return nil
	}
	return errors.New("refusing plain HTTP to a non-loopback server; use https or --insecure")
}

// Register creates an account and returns the recovery key, which must be
// shown to the user exactly once.
func Register(home, server, email string, password []byte, invite string) (string, error) {
	hc, err := HTTPClient(home)
	if err != nil {
		return "", err
	}
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	salt := crypto.RandomBytes(16)
	uk, err := crypto.DeriveUserKeys(password, salt, KDF)
	if err != nil {
		return "", err
	}
	vaultKey, recovery := crypto.NewVaultKey(), crypto.RandomBytes(crypto.KeySize)
	byKEK, byRec, err := crypto.WrapVaultKey(vaultKey, uk.KEK, recovery)
	if err != nil {
		return "", err
	}
	err = NewAPI(server, "", hc).Register(api.RegisterRequest{
		Email: strings.TrimSpace(email), KDF: KDF, Salt: salt, AuthKey: uk.AuthKey,
		EncVaultKey: byKEK, EncVaultKeyRecovery: byRec, InviteCode: invite, RecoveryAuth: crypto.RecoveryAuth(recovery),
	})
	if err != nil {
		return "", err
	}
	return crypto.EncodeRecoveryKey(recovery), nil
}

// Login authenticates, registers this device and caches the vault key.
// ErrOtherLogin: this hearth home is logged in to another account or
// server; logging in again would replace its keys and sync state owner.
type ErrOtherLogin struct{ Email, Server, Home string }

func (e ErrOtherLogin) Error() string {
	return fmt.Sprintf("%s is logged in as %s on %s; to replace that login pass --switch (or use another HEARTH_HOME / directory)", e.Home, e.Email, e.Server)
}

// CheckSwitch returns ErrOtherLogin when home holds a login for a
// different account or server than the one requested.
func CheckSwitch(home, server, email string) error {
	cfg, err := LoadConfig(home)
	if err != nil {
		return nil
	}
	if strings.EqualFold(cfg.Email, email) && strings.TrimRight(cfg.Server, "/") == strings.TrimRight(server, "/") {
		return nil
	}
	return ErrOtherLogin{Email: cfg.Email, Server: cfg.Server, Home: home}
}

// ErrBadLogin: no account with this email and password (the server does
// not say which of the two is wrong).
var ErrBadLogin = errors.New("invalid email or password")

func Login(home, server, email string, password []byte, deviceName string) (*Config, error) {
	if err := adoptCA(home); err != nil {
		return nil, err
	}
	hc, err := HTTPClient(home)
	if err != nil {
		return nil, err
	}
	a := NewAPI(server, "", hc)
	pre, err := a.Prelogin(email)
	if err != nil {
		return nil, err
	}
	// Validate before running Argon2id so a hostile server cannot make the
	// client derive keys with weakened parameters.
	uk, err := crypto.DeriveUserKeys(password, pre.Salt, pre.KDF)
	if err != nil {
		return nil, err
	}
	resp, err := a.Login(api.LoginRequest{Email: email, AuthKey: uk.AuthKey, DeviceName: deviceName})
	if err != nil {
		if StatusOf(err) == 401 {
			return nil, ErrBadLogin
		}
		return nil, err
	}
	vk, err := crypto.UnwrapWithKEK(resp.EncVaultKey, uk.KEK)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt vault key: %w", err)
	}
	if err := saveVaultKey(home, vk); err != nil {
		return nil, err
	}
	cfg := &Config{Server: strings.TrimRight(server, "/"), Email: email, DeviceID: resp.DeviceID,
		DeviceName: deviceName, Token: resp.Token, ExpiresAt: resp.ExpiresAt}
	return cfg, SaveConfig(home, cfg)
}

// newCredentials derives password material for an existing vault key.
func newCredentials(password, vaultKey []byte) (*api.NewCredentials, error) {
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	salt := crypto.RandomBytes(16)
	uk, err := crypto.DeriveUserKeys(password, salt, KDF)
	if err != nil {
		return nil, err
	}
	wrapped, err := crypto.WrapWithKEK(vaultKey, uk.KEK)
	if err != nil {
		return nil, err
	}
	return &api.NewCredentials{KDF: KDF, Salt: salt, AuthKey: uk.AuthKey, EncVaultKey: wrapped}, nil
}

func (a *API) ChangePassword(req api.ChangePasswordRequest) error {
	_, err := a.do("POST", "/v1/auth/password", req, nil, nil, nil)
	return err
}

func (a *API) Recover(req api.RecoveryRequest) (api.RecoveryResponse, error) {
	var r api.RecoveryResponse
	_, err := a.do("POST", "/v1/auth/recover", req, nil, nil, &r)
	return r, err
}

// ChangePassword re-wraps the vault key under a new password. Other devices
// keep working: the vault key and their tokens are unchanged.
func (e *Engine) ChangePassword(oldPw, newPw []byte) error {
	pre, err := e.API.Prelogin(e.Cfg.Email)
	if err != nil {
		return err
	}
	old, err := crypto.DeriveUserKeys(oldPw, pre.Salt, pre.KDF)
	if err != nil {
		return err
	}
	n, err := newCredentials(newPw, e.Vault.Key())
	if err != nil {
		return err
	}
	err = e.API.ChangePassword(api.ChangePasswordRequest{OldAuthKey: old.AuthKey, New: *n})
	if StatusOf(err) == 401 {
		return errors.New("current password is wrong")
	}
	return err
}

// Recover sets a new password using the recovery key (forgotten password),
// then logs this machine in. All existing device tokens are revoked.
func Recover(home, server, email, recoveryKey string, newPw []byte, device string) (*Config, error) {
	rk, err := crypto.DecodeRecoveryKey(recoveryKey)
	if err != nil {
		return nil, err
	}
	hc, err := HTTPClient(home)
	if err != nil {
		return nil, err
	}
	a := NewAPI(server, "", hc)
	req := api.RecoveryRequest{Email: email, RecoveryAuth: crypto.RecoveryAuth(rk)}
	r, err := a.Recover(req)
	if StatusOf(err) == 401 {
		return nil, errors.New("invalid email or recovery key")
	}
	if err != nil {
		return nil, err
	}
	vk, err := crypto.UnwrapWithRecovery(r.EncVaultKeyRecovery, rk)
	if err != nil {
		return nil, fmt.Errorf("recovery key does not decrypt the vault: %w", err)
	}
	if req.New, err = newCredentials(newPw, vk); err != nil {
		return nil, err
	}
	if _, err := a.Recover(req); err != nil {
		return nil, err
	}
	return Login(home, server, email, newPw, device)
}

// RotateRecoveryKey issues a new recovery key and invalidates the old one
// (e.g. it was pasted somewhere). Needs the master password; devices and
// the vault key are unaffected. Returns the new key, to be shown once.
func (e *Engine) RotateRecoveryKey(password []byte) (string, error) {
	pre, err := e.API.Prelogin(e.Cfg.Email)
	if err != nil {
		return "", err
	}
	uk, err := crypto.DeriveUserKeys(password, pre.Salt, pre.KDF)
	if err != nil {
		return "", err
	}
	recovery := crypto.RandomBytes(crypto.KeySize)
	_, byRec, err := crypto.WrapVaultKey(e.Vault.Key(), uk.KEK, recovery)
	if err != nil {
		return "", err
	}
	_, err = e.API.do("POST", "/v1/auth/recovery-key", api.RotateRecoveryRequest{AuthKey: uk.AuthKey,
		EncVaultKeyRecovery: byRec, RecoveryAuth: crypto.RecoveryAuth(recovery)}, nil, nil, nil)
	if StatusOf(err) == 401 {
		return "", errors.New("password is wrong")
	}
	if err != nil {
		return "", err
	}
	return crypto.EncodeRecoveryKey(recovery), nil
}
