package client

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestChangePasswordAndRecovery(t *testing.T) {
	en := newEnv(t)
	inv, _ := en.srv.CreateInvite(time.Hour)
	rk, err := Register(t.TempDir(), en.url, "carol@example.com", []byte(pw), inv)
	if err != nil {
		t.Fatal(err)
	}
	h1, h2 := t.TempDir(), t.TempDir()
	if _, err := Login(h1, en.url, "carol@example.com", []byte(pw), "laptop"); err != nil {
		t.Fatal(err)
	}
	if _, err := Login(h2, en.url, "carol@example.com", []byte(pw), "desktop"); err != nil {
		t.Fatal(err)
	}
	e1, _ := NewEngine(h1)
	vk := e1.Vault.Key()

	// Change password: old one required, other devices keep working.
	if err := e1.ChangePassword([]byte("wrong-old-password"), []byte("new password 1")); err == nil || !strings.Contains(err.Error(), "wrong") {
		t.Fatalf("wrong old password: %v", err)
	}
	if err := e1.ChangePassword([]byte(pw), []byte("new password 1")); err != nil {
		t.Fatal(err)
	}
	if _, err := Login(t.TempDir(), en.url, "carol@example.com", []byte(pw), "x"); err == nil {
		t.Fatal("old password still works")
	}
	h3 := t.TempDir()
	if _, err := Login(h3, en.url, "carol@example.com", []byte("new password 1"), "x"); err != nil {
		t.Fatal(err)
	}
	if e3, _ := NewEngine(h3); !bytes.Equal(e3.Vault.Key(), vk) {
		t.Fatal("vault key changed with the password")
	}
	e2, _ := NewEngine(h2)
	if _, err := e2.API.Sessions(); err != nil {
		t.Fatalf("other device lost access after password change: %v", err)
	}

	// Forgotten password: recovery key sets a new one, same vault, all tokens revoked.
	if _, err := Recover(t.TempDir(), en.url, "carol@example.com", "AAAA-BBBB", []byte("x"), "y"); err == nil {
		t.Fatal("malformed recovery key accepted")
	}
	other, _ := Register(t.TempDir(), en.url, "dave@example.com", []byte(pw), mustInvite(t, en))
	if _, err := Recover(t.TempDir(), en.url, "carol@example.com", other, []byte("new password 2"), "y"); err == nil {
		t.Fatal("another user's recovery key accepted")
	}
	h4 := t.TempDir()
	if _, err := Recover(h4, en.url, "carol@example.com", rk, []byte("new password 2"), "phone"); err != nil {
		t.Fatal(err)
	}
	if e4, _ := NewEngine(h4); !bytes.Equal(e4.Vault.Key(), vk) {
		t.Fatal("recovered a different vault key")
	}
	if _, err := e2.API.Sessions(); StatusOf(err) != 401 {
		t.Fatalf("old device token survived recovery: %v", err)
	}
	if _, err := Login(t.TempDir(), en.url, "carol@example.com", []byte("new password 1"), "x"); err == nil {
		t.Fatal("pre-recovery password still works")
	}
}

func mustInvite(t *testing.T, en *env) string {
	t.Helper()
	inv, err := en.srv.CreateInvite(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

// A leaked recovery key is replaced; the old one stops working, devices
// keep theirs, and the new key recovers the same vault.
func TestRotateRecoveryKey(t *testing.T) {
	en := newEnv(t)
	inv, _ := en.srv.CreateInvite(time.Hour)
	old, err := Register(t.TempDir(), en.url, "erin@example.com", []byte(pw), inv)
	if err != nil {
		t.Fatal(err)
	}
	h := t.TempDir()
	Login(h, en.url, "erin@example.com", []byte(pw), "laptop")
	e, _ := NewEngine(h)
	if _, err := e.RotateRecoveryKey([]byte("wrong password!")); err == nil {
		t.Fatal("rotated with a wrong password")
	}
	rk, err := e.RotateRecoveryKey([]byte(pw))
	if err != nil || rk == old {
		t.Fatalf("%q %v", rk, err)
	}
	if _, err := e.API.Sessions(); err != nil {
		t.Fatalf("device lost access: %v", err)
	}
	if _, err := Recover(t.TempDir(), en.url, "erin@example.com", old, []byte("new password 9"), "x"); err == nil {
		t.Fatal("old recovery key still works")
	}
	h2 := t.TempDir()
	if _, err := Recover(h2, en.url, "erin@example.com", rk, []byte("new password 9"), "x"); err != nil {
		t.Fatal(err)
	}
	if e2, _ := NewEngine(h2); !bytes.Equal(e2.Vault.Key(), e.Vault.Key()) {
		t.Fatal("new recovery key opens a different vault")
	}
}
