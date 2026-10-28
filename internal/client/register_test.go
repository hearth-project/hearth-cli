package client

import (
	"io"
	"log"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/server"
	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

func TestRegistrationModes(t *testing.T) {
	newSrv := func(mode string, now func() time.Time) (*server.Server, string) {
		dbURL, blobs := servertest.Backends(t)
		srv, err := server.New(server.Config{DataDir: t.TempDir(), Database: dbURL, Blobs: blobs, Registration: mode, Now: now, Logger: log.New(io.Discard, "", 0)})
		if err != nil {
			t.Fatal(err)
		}
		hs := httptest.NewServer(srv)
		t.Cleanup(func() { hs.Close(); srv.Close() })
		return srv, hs.URL
	}
	newEnv(t) // sets the cheap test KDF

	c := &clock{t: time.Unix(1_800_000_000, 0)}
	srv, url := newSrv("", c.now) // default = invite
	if _, err := Register(t.TempDir(), url, "a@example.com", []byte(pw), ""); StatusOf(err) != 403 {
		t.Fatalf("no invite: got %v, want 403", err)
	}
	if _, err := Register(t.TempDir(), url, "a@example.com", []byte(pw), "inv-bogus"); StatusOf(err) != 403 {
		t.Fatalf("bogus invite: got %v, want 403", err)
	}
	code, _ := srv.CreateInvite(time.Hour)
	if _, err := Register(t.TempDir(), url, "a@example.com", []byte(pw), code); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(t.TempDir(), url, "b@example.com", []byte(pw), code); StatusOf(err) != 403 {
		t.Fatalf("reused invite: got %v, want 403", err)
	}
	// A failed registration (duplicate email) must not burn the invite.
	code2, _ := srv.CreateInvite(time.Hour)
	if _, err := Register(t.TempDir(), url, "a@example.com", []byte(pw), code2); StatusOf(err) != 409 {
		t.Fatalf("duplicate email: got %v, want 409", err)
	}
	if _, err := Register(t.TempDir(), url, "b@example.com", []byte(pw), code2); err != nil {
		t.Fatalf("invite burned by failed registration: %v", err)
	}
	code3, _ := srv.CreateInvite(time.Hour)
	c.add(2 * time.Hour)
	if _, err := Register(t.TempDir(), url, "c@example.com", []byte(pw), code3); StatusOf(err) != 403 {
		t.Fatalf("expired invite: got %v, want 403", err)
	}

	_, url = newSrv(server.RegistrationClosed, nil)
	if _, err := Register(t.TempDir(), url, "a@example.com", []byte(pw), ""); StatusOf(err) != 403 {
		t.Fatalf("closed: got %v, want 403", err)
	}
	_, url = newSrv(server.RegistrationOpen, nil)
	if _, err := Register(t.TempDir(), url, "a@example.com", []byte(pw), ""); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := server.New(server.Config{DataDir: t.TempDir(), Registration: "bogus"}); err == nil {
		t.Fatal("unknown registration mode accepted")
	}
}
