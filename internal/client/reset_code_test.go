package client

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/adapter"
	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
)

func operatorKey(t *testing.T) (*ecdh.PrivateKey, string) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k, base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
}

func TestResetCodePreservesVault(t *testing.T) {
	en := newEnv(t)
	m := en.machine("original")
	const email = "dev@example.com"
	k, pub := operatorKey(t)
	if _, err := en.srv.IssueResetCode(email, k); err == nil {
		t.Fatal("issued without enrollment")
	}
	if err := m.SetRecoveryDelegate([]byte("wrong password"), pub); StatusOf(err) != 401 {
		t.Fatalf("wrong password: %v", err)
	}
	if err := m.SetRecoveryDelegate([]byte(pw), pub); err != nil {
		t.Fatal(err)
	}
	wrong, _ := operatorKey(t)
	if _, err := en.srv.IssueResetCode(email, wrong); err == nil {
		t.Fatal("wrong operator key accepted")
	}
	m.appendLines(t, 14, "before reset")
	m.push(t)
	if err := m.SetSecret(Secret{Name: "PERSONAL", Value: []byte("test fixture")}); err != nil {
		t.Fatal(err)
	}
	if err := m.API.CreateTeam("fixture"); err != nil {
		t.Fatal(err)
	}
	const other = "other@example.com"
	otherHome := t.TempDir()
	if _, err := Register(otherHome, en.url, other, []byte(pw), mustInvite(t, en)); err != nil {
		t.Fatal(err)
	}
	if _, err := Login(otherHome, en.url, other, []byte(pw), "other"); err != nil {
		t.Fatal(err)
	}
	code, err := en.srv.IssueResetCode(email, k)
	if err != nil {
		t.Fatal(err)
	}
	next := []byte("replacement password")
	if err := ResetPasswordWithCode(otherHome, en.url, other, code, next); StatusOf(err) != 401 {
		t.Fatalf("wrong target email: %v", err)
	}
	if err := ResetPasswordWithCode(otherHome, en.url, "Dev@Example.Com", code, next); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(otherHome)
	if err != nil || cfg.Email != other {
		t.Fatal("redeemer's own login was changed")
	}
	if err := ResetPasswordWithCode(t.TempDir(), en.url, email, code, next); StatusOf(err) != 401 {
		t.Fatalf("code replay: %v", err)
	}
	home := t.TempDir()
	if _, err := Login(home, en.url, email, []byte(pw), "new"); err == nil {
		t.Fatal("old password survived")
	}
	if _, err := Login(home, en.url, email, next, "new"); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(e.Vault.Key(), m.Vault.Key()) {
		t.Fatal("vault changed")
	}
	secret, err := e.Secret("PERSONAL")
	if err != nil || string(secret.Value) != "test fixture" {
		t.Fatal("old credential unavailable")
	}
	teams, err := e.API.Teams()
	if err != nil || len(teams) != 1 || teams[0].Role != "owner" {
		t.Fatal("team ownership changed")
	}
	restored := &machine{Engine: e, codex: t.TempDir()}
	restored.Adapters = []adapter.Adapter{adapter.CodexAt(restored.codex)}
	if _, err := restored.Restore(restored.only(t), false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.read(t), m.read(t)) {
		t.Fatal("restored session differs")
	}
	if _, err := m.API.Devices(); err != nil {
		t.Fatalf("lost existing device: %v", err)
	}
}

func TestResetCodeLifecycle(t *testing.T) {
	en := newEnv(t)
	m := en.machine("original")
	const email = "dev@example.com"
	k, pub := operatorKey(t)
	if err := m.SetRecoveryDelegate([]byte(pw), pub); err != nil {
		t.Fatal(err)
	}
	issue := func() string {
		t.Helper()
		v, err := en.srv.IssueResetCode(email, k)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	a := NewAPI(en.url, "", m.API.HC)
	request := func(code string) api.ResetCodeRequest {
		raw, err := crypto.DecodeRecoveryKey(code)
		if err != nil {
			t.Fatal(err)
		}
		return api.ResetCodeRequest{Email: email, Proof: crypto.ResetAuth(raw), Claim: crypto.RandomBytes(32)}
	}
	claim := func(r api.ResetCodeRequest) error {
		_, err := a.do("POST", "/v1/auth/reset-code", r, nil, nil, &api.ResetCodeResponse{})
		return err
	}
	reject := func(r api.ResetCodeRequest) {
		t.Helper()
		if err := claim(r); StatusOf(err) != 401 {
			t.Fatalf("grant accepted or unexpected error: %v", err)
		}
	}
	old := request(issue())
	r := request(issue())
	reject(old)
	en.clock.add(30 * time.Minute)
	reject(r)
	r = request(issue())
	if err := en.srv.SetUserStatus(email, false); err != nil {
		t.Fatal(err)
	}
	reject(r)
	if err := en.srv.SetUserStatus(email, true); err != nil {
		t.Fatal(err)
	}
	r = request(issue())
	competing := r
	competing.Claim = crypto.RandomBytes(32)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, v := range []api.ResetCodeRequest{r, competing} {
		wg.Add(1)
		go func() { defer wg.Done(); results <- claim(v) }()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if StatusOf(err) != 401 {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("expected exactly one claimant")
	}
	r = request(issue())
	if err := claim(r); err != nil {
		t.Fatal(err)
	}
	if err := m.SetRecoveryDelegate([]byte(pw), ""); err != nil {
		t.Fatal(err)
	}
	reject(r)
	if err := m.SetRecoveryDelegate([]byte(pw), pub); err != nil {
		t.Fatal(err)
	}
	r = request(issue())
	if err := m.ChangePassword([]byte(pw), []byte("newer password")); err != nil {
		t.Fatal(err)
	}
	reject(r)
}
