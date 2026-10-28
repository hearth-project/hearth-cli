package server

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

func testBlobStore(t *testing.T, b blobStore) {
	t.Helper()
	if err := b.put("1/s/0-0", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got, err := b.get("1/s/0-0"); err != nil || string(got) != "hello" {
		t.Fatalf("get: %q %v", got, err)
	}
	if err := b.put("1/s/0-0", []byte("again")); err != nil { // retry overwrites
		t.Fatal(err)
	}
	if got, _ := b.get("1/s/0-0"); string(got) != "again" {
		t.Fatalf("overwrite: %q", got)
	}
	if err := b.del("1/s/0-0"); err != nil {
		t.Fatal(err)
	}
	if err := b.del("1/s/0-0"); err != nil {
		t.Fatalf("deleting a missing blob: %v", err)
	}
	if _, err := b.get("1/s/0-0"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing blob: %v", err)
	}
}

func TestLocalBlobs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "objects")
	b, err := openBlobs("file://" + dir)
	if err != nil {
		t.Fatal(err)
	}
	testBlobStore(t, b)
}

func TestS3Blobs(t *testing.T) {
	backend := s3mem.New()
	backend.CreateBucket("bkt")
	hs := httptest.NewServer(gofakes3.New(backend).Server())
	defer hs.Close()
	t.Setenv("HEARTH_S3_ACCESS_KEY", "k")
	t.Setenv("HEARTH_S3_SECRET_KEY", "s")
	if _, err := openBlobs("s3://nope?endpoint=" + hs.URL + "&path_style=1"); err == nil {
		t.Fatal("missing bucket accepted")
	}
	b, err := openBlobs("s3://bkt/pre/fix?endpoint=" + hs.URL + "&path_style=1&region=us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	testBlobStore(t, b)
	b.put("x", []byte("1"))
	if _, err := backend.HeadObject("bkt", "pre/fix/x"); err != nil {
		t.Fatalf("prefix not applied: %v", err)
	}
}

func TestRebind(t *testing.T) {
	got := postgresDialect.rebind(`SELECT '?' , a FROM t WHERE x=? AND y IN (?,?)`)
	if got != `SELECT '?' , a FROM t WHERE x=$1 AND y IN ($2,$3)` {
		t.Fatal(got)
	}
	if sqliteDialect.rebind("x=?") != "x=?" {
		t.Fatal("sqlite rewritten")
	}
}
