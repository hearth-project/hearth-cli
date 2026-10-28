package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
)

// AAD labels for wrapped vault keys.
const (
	aadVaultKEK      = "hearth/vault-key/kek/v1"
	aadVaultRecovery = "hearth/vault-key/recovery/v1"
	aadVaultIdentity = "hearth/vault-key/identity/v1"
)

// Vault holds the decrypted vault key and its derived subkeys.
type Vault struct {
	key     []byte
	pathKey []byte
	metaKey []byte
}

// NewVaultKey generates a fresh random vault key.
func NewVaultKey() []byte { return RandomBytes(KeySize) }

// OpenVault wraps an existing raw vault key.
func OpenVault(key []byte) (*Vault, error) {
	if len(key) != KeySize {
		return nil, errors.New("crypto: bad vault key size")
	}
	k := append([]byte(nil), key...)
	return &Vault{key: k, pathKey: HKDF(k, "hearth-path-index"), metaKey: HKDF(k, "hearth-meta")}, nil
}

// Key returns the raw vault key (for local caching only).
func (v *Vault) Key() []byte { return append([]byte(nil), v.key...) }

// WrapVaultKey encrypts the vault key with the KEK and the recovery key.
func WrapVaultKey(vaultKey, kek, recoveryKey []byte) (byKEK, byRecovery []byte, err error) {
	if byKEK, err = Seal(kek, vaultKey, []byte(aadVaultKEK)); err != nil {
		return nil, nil, err
	}
	if byRecovery, err = Seal(recoveryKey, vaultKey, []byte(aadVaultRecovery)); err != nil {
		return nil, nil, err
	}
	return byKEK, byRecovery, nil
}

// WrapWithKEK encrypts the vault key with a (new) password-derived KEK.
func WrapWithKEK(vaultKey, kek []byte) ([]byte, error) {
	return Seal(kek, vaultKey, []byte(aadVaultKEK))
}

// RecoveryAuth derives the value that proves possession of the recovery key
// to the server without revealing the key itself.
func RecoveryAuth(recoveryKey []byte) []byte { return HKDF(recoveryKey, "hearth-recovery-auth") }

// UnwrapWithKEK decrypts the vault key using the password-derived KEK.
func UnwrapWithKEK(wrapped, kek []byte) ([]byte, error) {
	return Open(kek, wrapped, []byte(aadVaultKEK))
}

// UnwrapWithRecovery decrypts the vault key using the recovery key.
func UnwrapWithRecovery(wrapped, recoveryKey []byte) ([]byte, error) {
	return Open(recoveryKey, wrapped, []byte(aadVaultRecovery))
}

// IdentityKey derives the vault-unlock key from a platform credential (for
// example the PDC secret key injected into a job container).
func IdentityKey(secret []byte) []byte { return HKDF(secret, "hearth-identity-key") }

// IdentityAuth is what the server stores to verify possession of that key.
func IdentityAuth(key []byte) []byte { return HKDF(key, "hearth-identity-auth") }

// IdentityProof signs a login message with the identity key.
func IdentityProof(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// WrapWithIdentity encrypts the vault key for passwordless identity login.
func WrapWithIdentity(vaultKey, key []byte) ([]byte, error) {
	return Seal(key, vaultKey, []byte(aadVaultIdentity))
}

// UnwrapWithIdentity reverses WrapWithIdentity.
func UnwrapWithIdentity(wrapped, key []byte) ([]byte, error) {
	return Open(key, wrapped, []byte(aadVaultIdentity))
}

// SessionID returns the opaque server-side identifier for a logical
// session path such as "codex/sessions/2026/10/22/rollout-x.jsonl".
func (v *Vault) SessionID(logical string) string {
	return MAC(v.pathKey, "session:"+logical)
}

// SealMeta encrypts session metadata bound to its session ID.
func (v *Vault) SealMeta(sessionID string, meta []byte) ([]byte, error) {
	return Seal(v.metaKey, meta, []byte("hearth/meta/v1/"+sessionID))
}

// OpenMeta decrypts session metadata.
func (v *Vault) OpenMeta(sessionID string, data []byte) ([]byte, error) {
	return Open(v.metaKey, data, []byte("hearth/meta/v1/"+sessionID))
}

func chunkAAD(sessionID string, gen, offset int64, length int) []byte {
	return []byte(fmt.Sprintf("hearth/chunk/v1/%s/%d/%d/%d", sessionID, gen, offset, length))
}

func (v *Vault) sessionKey(sessionID string) []byte {
	return HKDF(v.key, "hearth-session:"+sessionID)
}

// SealChunk encrypts one append chunk. The AAD binds it to its session,
// generation and byte range so the server cannot reorder or move chunks.
func (v *Vault) SealChunk(sessionID string, gen, offset int64, plain []byte) ([]byte, error) {
	return Seal(v.sessionKey(sessionID), plain, chunkAAD(sessionID, gen, offset, len(plain)))
}

// OpenChunk decrypts one chunk; length is the expected plaintext length.
func (v *Vault) OpenChunk(sessionID string, gen, offset int64, length int, data []byte) ([]byte, error) {
	return Open(v.sessionKey(sessionID), data, chunkAAD(sessionID, gen, offset, length))
}

// SecretID is the opaque server-side id of a named secret.
func (v *Vault) SecretID(name string) string { return MAC(v.pathKey, "secret:"+name) }

func (v *Vault) secretKey() []byte { return HKDF(v.key, "hearth-secret") }

// SealSecret encrypts a secret payload bound to its id.
func (v *Vault) SealSecret(id string, payload []byte) ([]byte, error) {
	return Seal(v.secretKey(), payload, []byte("hearth/secret/v1/"+id))
}

// OpenSecret decrypts a secret payload.
func (v *Vault) OpenSecret(id string, data []byte) ([]byte, error) {
	return Open(v.secretKey(), data, []byte("hearth/secret/v1/"+id))
}
