// Package servertest selects storage backends for tests, so the whole
// suite can run against SQLite + a local directory (default), PostgreSQL
// and/or S3:
//
//	HEARTH_TEST_POSTGRES=postgres://user@host/postgres   a fresh database per test
//	HEARTH_TEST_S3=fake                                  in-process S3 (gofakes3)
//	HEARTH_TEST_S3=s3://bucket?endpoint=http://host:9000&path_style=1   real S3 / MinIO
package servertest

import (
	"database/sql"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

// Backends returns the Database and Blobs settings for a test server; both
// are "" (SQLite and a local directory in DataDir) unless overridden.
func Backends(tb testing.TB) (database, blobs string) {
	tb.Helper()
	return Postgres(tb), S3(tb)
}

// LocalBlobs reports whether blobs are files under DataDir/objects.
func LocalBlobs() bool { return os.Getenv("HEARTH_TEST_S3") == "" }

var seq struct {
	sync.Mutex
	n int
}

func uniq() string {
	seq.Lock()
	defer seq.Unlock()
	seq.n++
	return fmt.Sprintf("t%d_%d_%d", os.Getpid(), time.Now().UnixNano()%1e9, seq.n)
}

// Postgres creates a fresh database and drops it when the test ends.
func Postgres(tb testing.TB) string {
	admin := os.Getenv("HEARTH_TEST_POSTGRES")
	if admin == "" {
		return ""
	}
	db, err := sql.Open("pgx", admin)
	if err != nil {
		tb.Fatal(err)
	}
	name := "hearth_" + uniq()
	if _, err := db.Exec("CREATE DATABASE " + name); err != nil {
		db.Close()
		tb.Fatalf("create test database: %v", err)
	}
	tb.Cleanup(func() {
		db.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		db.Close()
	})
	u, err := url.Parse(admin)
	if err != nil {
		tb.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

var fake struct {
	once sync.Once
	url  string
}

// S3 returns a blob URL with a fresh prefix.
func S3(tb testing.TB) string {
	v := os.Getenv("HEARTH_TEST_S3")
	switch {
	case v == "":
		return ""
	case v == "fake":
		fake.once.Do(func() {
			backend := s3mem.New()
			backend.CreateBucket("hearth")
			hs := httptest.NewServer(gofakes3.New(backend).Server())
			os.Setenv("HEARTH_S3_ACCESS_KEY", "test")
			os.Setenv("HEARTH_S3_SECRET_KEY", "testsecret")
			fake.url = "s3://hearth?endpoint=" + hs.URL + "&path_style=1&region=us-east-1"
		})
		v = fake.url
	}
	base, q, _ := strings.Cut(v, "?")
	return strings.TrimRight(base, "/") + "/" + uniq() + "?" + q
}
