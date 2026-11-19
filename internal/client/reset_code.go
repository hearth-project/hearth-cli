package client

import (
	"encoding/base64"
	"errors"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// SetRecoveryDelegate opts this account into recovery with a specific operator.
// Clearing the public key also withdraws any issued code.
func (e *Engine) SetRecoveryDelegate(password []byte, publicKey string) error {
	pre, err := e.API.Prelogin(e.Cfg.Email)
	if err != nil {
		return err
	}
	uk, err := crypto.DeriveUserKeys(password, pre.Salt, pre.KDF)
	if err != nil {
		return err
	}
	req := api.RecoveryDelegationRequest{AuthKey: uk.AuthKey}
	if publicKey != "" {
		key, err := base64.RawURLEncoding.DecodeString(publicKey)
		if err != nil || len(key) != 32 {
			return errors.New("invalid operator public key")
		}
		// Verify the original server-backed vault rather than trusting a cached key.
		var current api.ResetCodeResponse
		if _, err = e.API.do("POST", "/v1/auth/recovery-delegation/vault", req, nil, nil, &current); err != nil {
			return err
		}
		vault, err := crypto.UnwrapWithKEK(current.Envelope, uk.KEK)
		if err != nil {
			return err
		}
		defer clear(vault)
		if len(vault) != crypto.KeySize {
			return errors.New("invalid vault key")
		}
		req.Envelope, err = crypto.DelegateVault(e.Cfg.Email, vault, key)
		if err != nil {
			return err
		}
	}
	_, err = e.API.do("PUT", "/v1/auth/recovery-delegation", req, nil, nil, nil)
	return err
}

// ResetPasswordWithCode runs without a device session and does not replace
// any local login. The same claim is used in both halves of this redemption.
func ResetPasswordWithCode(home, server, email, code string, newPw []byte) error {
	if len(newPw) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	raw, err := crypto.DecodeRecoveryKey(code)
	if err != nil {
		return errors.New("invalid reset code format")
	}
	defer clear(raw)
	hc, err := HTTPClient(home)
	if err != nil {
		return err
	}
	a := NewAPI(server, "", hc)
	req := api.ResetCodeRequest{Email: email, Proof: crypto.ResetAuth(raw), Claim: crypto.RandomBytes(crypto.KeySize)}
	var resp api.ResetCodeResponse
	const path = "/v1/auth/reset-code"
	if _, err = a.do("POST", path, req, nil, nil, &resp); err != nil {
		return err
	}
	vault, err := crypto.OpenForReset(email, resp.Envelope, raw)
	if err != nil {
		return err
	}
	defer clear(vault)
	if len(vault) != crypto.KeySize {
		return errors.New("reset envelope contains an invalid vault key")
	}
	if req.New, err = newCredentials(newPw, vault); err != nil {
		return err
	}
	_, err = a.do("POST", path, req, nil, nil, nil)
	return err
}
