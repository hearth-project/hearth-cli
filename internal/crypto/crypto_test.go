package crypto

import (
	"bytes"
	"errors"
	"testing"
)

// testKDF keeps tests fast while still passing Validate.
var testKDF = KDFParams{Time: 1, MemoryKiB: 19 * 1024, Threads: 1}

func TestDeriveUserKeysDeterministicAndSeparated(t *testing.T) {
	salt := bytes.Repeat([]byte{7}, 16)
	a, err := DeriveUserKeys([]byte("pw"), salt, testKDF)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := DeriveUserKeys([]byte("pw"), salt, testKDF)
	if !bytes.Equal(a.AuthKey, b.AuthKey) || !bytes.Equal(a.KEK, b.KEK) {
		t.Fatal("derivation not deterministic")
	}
	if bytes.Equal(a.AuthKey, a.KEK) {
		t.Fatal("auth key must differ from KEK")
	}
	c, _ := DeriveUserKeys([]byte("pw2"), salt, testKDF)
	if bytes.Equal(a.AuthKey, c.AuthKey) {
		t.Fatal("different passwords gave same auth key")
	}
}

func TestKDFValidateRejectsDowngrade(t *testing.T) {
	if _, err := DeriveUserKeys([]byte("pw"), make([]byte, 16), KDFParams{Time: 1, MemoryKiB: 8, Threads: 1}); err == nil {
		t.Fatal("weak KDF params accepted")
	}
	if _, err := DeriveUserKeys([]byte("pw"), make([]byte, 4), testKDF); err == nil {
		t.Fatal("short salt accepted")
	}
}

func TestSealOpenTamper(t *testing.T) {
	key := RandomBytes(KeySize)
	ct, err := Seal(key, []byte("hello"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := Open(key, ct, []byte("aad"))
	if err != nil || string(pt) != "hello" {
		t.Fatalf("roundtrip failed: %v %q", err, pt)
	}
	for i := range ct {
		bad := append([]byte(nil), ct...)
		bad[i] ^= 1
		if _, err := Open(key, bad, []byte("aad")); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("tampered byte %d accepted", i)
		}
	}
	if _, err := Open(key, ct, []byte("other")); !errors.Is(err, ErrDecrypt) {
		t.Fatal("wrong AAD accepted")
	}
	if _, err := Open(RandomBytes(KeySize), ct, []byte("aad")); !errors.Is(err, ErrDecrypt) {
		t.Fatal("wrong key accepted")
	}
	if _, err := Open(key, ct[:10], nil); !errors.Is(err, ErrDecrypt) {
		t.Fatal("truncated ciphertext accepted")
	}
}

func TestVaultKeyWrapping(t *testing.T) {
	vk := NewVaultKey()
	kek, rk := RandomBytes(KeySize), RandomBytes(KeySize)
	byKEK, byRec, err := WrapVaultKey(vk, kek, rk)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := UnwrapWithKEK(byKEK, kek); err != nil || !bytes.Equal(got, vk) {
		t.Fatal("KEK unwrap failed")
	}
	if got, err := UnwrapWithRecovery(byRec, rk); err != nil || !bytes.Equal(got, vk) {
		t.Fatal("recovery unwrap failed")
	}
	// The two wrappings use different AADs and must not be interchangeable.
	if _, err := UnwrapWithKEK(byRec, rk); err == nil {
		t.Fatal("recovery blob accepted as KEK blob")
	}
}

func TestIdentityKeyWrapsVaultKey(t *testing.T) {
	vk := NewVaultKey()
	key := IdentityKey([]byte("platform-secret"))
	wrapped, err := WrapWithIdentity(vk, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapWithIdentity(wrapped, IdentityKey([]byte("platform-secret")))
	if err != nil || !bytes.Equal(got, vk) {
		t.Fatalf("round trip: %x %v", got, err)
	}
	if _, err := UnwrapWithIdentity(wrapped, IdentityKey([]byte("other"))); err == nil {
		t.Fatal("wrong platform secret decrypted the vault")
	}
	p := IdentityProof(key, "msg")
	if len(p) != 32 || bytes.Equal(p, IdentityProof(IdentityKey([]byte("other")), "msg")) {
		t.Fatal("proof is not keyed by the identity key")
	}
}

func TestRecoveryKeyEncoding(t *testing.T) {
	k := RandomBytes(KeySize)
	s := EncodeRecoveryKey(k)
	got, err := DecodeRecoveryKey(" " + s + " ")
	if err != nil || !bytes.Equal(got, k) {
		t.Fatalf("decode failed: %v", err)
	}
	if _, err := DecodeRecoveryKey("ABCD"); err == nil {
		t.Fatal("short recovery key accepted")
	}
}

func TestChunkBinding(t *testing.T) {
	v, _ := OpenVault(NewVaultKey())
	sid := v.SessionID("codex/sessions/a.jsonl")
	if sid == v.SessionID("claude/projects/a.jsonl") {
		t.Fatal("session IDs collide")
	}
	ct, err := v.SealChunk(sid, 1, 100, []byte("line\n"))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := v.OpenChunk(sid, 1, 100, 5, ct); err != nil || string(pt) != "line\n" {
		t.Fatal("chunk roundtrip failed")
	}
	cases := []struct {
		sid         string
		gen, offset int64
		n           int
	}{{sid, 2, 100, 5}, {sid, 1, 0, 5}, {sid, 1, 100, 4}, {v.SessionID("x"), 1, 100, 5}}
	for _, c := range cases {
		if _, err := v.OpenChunk(c.sid, c.gen, c.offset, c.n, ct); err == nil {
			t.Fatalf("chunk accepted with wrong binding %+v", c)
		}
	}
	other, _ := OpenVault(NewVaultKey())
	if _, err := other.OpenChunk(sid, 1, 100, 5, ct); err == nil {
		t.Fatal("chunk opened with another vault")
	}
}
