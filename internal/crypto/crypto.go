// Package crypto implements hearth's client-side key hierarchy and
// authenticated encryption. The server never sees any key produced here
// except the auth key, which only proves knowledge of the master password.
package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// KeySize is the size of every symmetric key in bytes.
const KeySize = 32

// ErrDecrypt is returned when ciphertext fails authentication.
var ErrDecrypt = errors.New("crypto: decryption failed (wrong key or tampered data)")

// KDFParams are the Argon2id parameters used to derive the master key.
type KDFParams struct {
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
}

// DefaultKDF follows the design doc: m=64MiB, t=3, p=4.
var DefaultKDF = KDFParams{Time: 3, MemoryKiB: 64 * 1024, Threads: 4}

// Validate rejects parameters that are too weak or absurdly large.
// A malicious server could otherwise downgrade the KDF at prelogin.
func (p KDFParams) Validate() error {
	if p.Time < 1 || p.MemoryKiB < 19*1024 || p.Threads < 1 {
		return fmt.Errorf("crypto: KDF parameters too weak: %+v", p)
	}
	if p.MemoryKiB > 4*1024*1024 || p.Time > 100 {
		return fmt.Errorf("crypto: KDF parameters too large: %+v", p)
	}
	return nil
}

// RandomBytes returns n bytes from the OS CSPRNG.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic("crypto: OS random source failed: " + err.Error())
	}
	return b
}

// HKDF derives a KeySize-byte subkey bound to info.
func HKDF(secret []byte, info string) []byte {
	out := make([]byte, KeySize)
	r := hkdf.New(sha256.New, secret, nil, []byte(info))
	if _, err := io.ReadFull(r, out); err != nil {
		panic("crypto: hkdf failed: " + err.Error())
	}
	return out
}

// UserKeys are derived from the master password and never stored.
type UserKeys struct {
	AuthKey []byte // sent to the server to log in
	KEK     []byte // client only; wraps the vault key
}

// DeriveUserKeys runs Argon2id over the password and splits the result.
func DeriveUserKeys(password, salt []byte, p KDFParams) (UserKeys, error) {
	if err := p.Validate(); err != nil {
		return UserKeys{}, err
	}
	if len(salt) < 16 {
		return UserKeys{}, errors.New("crypto: salt too short")
	}
	master := argon2.IDKey(password, salt, p.Time, p.MemoryKiB, p.Threads, KeySize)
	return UserKeys{
		AuthKey: HKDF(master, "hearth-auth"),
		KEK:     HKDF(master, "hearth-kek"),
	}, nil
}

// Seal encrypts plaintext with XChaCha20-Poly1305. Output is nonce||ciphertext.
func Seal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := RandomBytes(aead.NonceSize())
	out := make([]byte, 0, len(nonce)+len(plaintext)+aead.Overhead())
	out = append(out, nonce...)
	return aead.Seal(out, nonce, plaintext, aad), nil
}

// Open reverses Seal; it fails if the key, AAD or data do not match.
func Open(key, data, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(data) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrDecrypt
	}
	nonce, ct := data[:aead.NonceSize()], data[aead.NonceSize():]
	pt, err := aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// MAC returns hex(HMAC-SHA256(key, msg)).
func MAC(key []byte, msg string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

var recoveryEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// EncodeRecoveryKey renders a key as dash-separated base32 groups.
func EncodeRecoveryKey(key []byte) string {
	s := recoveryEnc.EncodeToString(key)
	var groups []string
	for len(s) > 4 {
		groups = append(groups, s[:4])
		s = s[4:]
	}
	groups = append(groups, s)
	return strings.Join(groups, "-")
}

// DecodeRecoveryKey parses the output of EncodeRecoveryKey.
func DecodeRecoveryKey(s string) ([]byte, error) {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
	key, err := recoveryEnc.DecodeString(s)
	if err != nil || len(key) != KeySize {
		return nil, errors.New("crypto: invalid recovery key")
	}
	return key, nil
}
