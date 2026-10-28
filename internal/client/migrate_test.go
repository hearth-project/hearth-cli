package client

import (
	"bytes"
	"io"
	"log"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/server"
	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

// Data survives server migrate (to the configured test backends) and a
// backup into a plain SQLite directory; accounts, tokens, sessions,
// libraries and secrets all keep working against the new server.
func TestServerMigrateAndBackup(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	a.appendLines(t, 50, "migrate")
	a.push(t)
	if err := a.SetSecret(Secret{Name: "TOKEN", Value: []byte("s3cr3t")}); err != nil {
		t.Fatal(err)
	}
	want := a.read(t)

	dbURL, blobs := servertest.Backends(t)
	mid := server.Config{DataDir: t.TempDir(), Database: dbURL, Blobs: blobs}
	st, err := server.Migrate(en.cfg, mid, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if st.Blobs == 0 || st.Rows == 0 || st.MissingBlobs != 0 {
		t.Fatalf("stats %+v", st)
	}
	if _, err := server.Migrate(en.cfg, mid, nil); err == nil {
		t.Fatal("migrated into a non-empty destination")
	}
	backup := server.Config{DataDir: t.TempDir()}
	if _, err := server.Migrate(mid, backup, nil); err != nil {
		t.Fatal(err)
	}

	// The usual upgrade path: SQLite + directory -> PostgreSQL + S3 (when
	// configured for tests; otherwise another SQLite copy).
	upgraded := server.Config{DataDir: t.TempDir(), Database: servertest.Postgres(t), Blobs: servertest.S3(t)}
	if _, err := server.Migrate(backup, upgraded, nil); err != nil {
		t.Fatal(err)
	}

	for name, cfg := range map[string]server.Config{"migrated": mid, "backup": backup, "upgraded": upgraded} {
		cfg.Now, cfg.Logger, cfg.AuthPerMin, cfg.TokenTTL = en.clock.now, log.New(io.Discard, "", 0), 1000, 3650*24*time.Hour
		srv, err := server.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		hs := httptest.NewServer(srv)
		en2 := &env{t: t, url: hs.URL, srv: srv, clock: en.clock}
		b := en2.machine("b-" + name)
		if _, err := b.Restore(b.only(t), false); err != nil {
			t.Fatalf("%s: restore: %v", name, err)
		}
		if got := b.read(t); !bytes.Equal(got, want) {
			t.Fatalf("%s: restored %d bytes, want %d", name, len(got), len(want))
		}
		if s, err := b.Secret("TOKEN"); err != nil || string(s.Value) != "s3cr3t" {
			t.Fatalf("%s: secret %q %v", name, s.Value, err)
		}
		// New rows get fresh ids after explicit-id copies.
		if inv, err := srv.CreateInvite(time.Hour); err != nil {
			t.Fatal(err)
		} else if _, err := Register(t.TempDir(), hs.URL, "new-"+name+"@example.com", []byte(pw), inv); err != nil {
			t.Fatalf("%s: register after migrate: %v", name, err)
		}
		hs.Close()
		srv.Close()
	}
}
