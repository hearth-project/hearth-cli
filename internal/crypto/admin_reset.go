package crypto

import (
	"crypto/ecdh"
	"crypto/hpke"
	"strings"
)

// The operator keeps the private X25519 key offline. An account explicitly
// seals its vault key to the corresponding public key. Include the
// account name inside both envelopes so swapping rows cannot switch vaults.
func resetContext(email string) []byte {
	return []byte("hearth/admin-reset/v1/" + strings.ToLower(strings.TrimSpace(email)) + "\x00")
}

func DelegateVault(email string, vault []byte, pub []byte) ([]byte, error) {
	raw, err := ecdh.X25519().NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	pk, err := hpke.NewDHKEMPublicKey(raw)
	if err != nil {
		return nil, err
	}
	return hpke.Seal(pk, hpke.HKDFSHA256(), hpke.AES256GCM(), resetContext(email), vault)
}

func OpenDelegatedVault(email string, sealed []byte, priv *ecdh.PrivateKey) ([]byte, error) {
	sk, err := hpke.NewDHKEMPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return hpke.Open(sk, hpke.HKDFSHA256(), hpke.AES256GCM(), resetContext(email), sealed)
}

func ResetAuth(code []byte) []byte { return HKDF(code, "hearth/admin-reset/auth/v1") }
func WrapForReset(email string, vault, code []byte) ([]byte, error) {
	return Seal(HKDF(code, "hearth/admin-reset/wrap/v1"), vault, resetContext(email))
}
func OpenForReset(email string, wrapped, code []byte) ([]byte, error) {
	return Open(HKDF(code, "hearth/admin-reset/wrap/v1"), wrapped, resetContext(email))
}
