package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// blobStore holds immutable ciphertext chunks and plaintext bundles. Names
// are relative slash paths; the metadata database is the source of truth
// for which blobs exist.
type blobStore interface {
	put(name string, data []byte) error
	get(name string) ([]byte, error)
	del(name string) error // missing blobs are not an error
	// list calls fn for every stored blob (admin sweep of orphans).
	list(fn func(name string, modified time.Time, size int64) error) error
	String() string
}

// openBlobs parses a blob URL:
//
//	file:///var/lib/hearth/objects  (or a plain path)
//	s3://bucket/prefix?endpoint=minio:9000&region=us-east-1&insecure=1&path_style=1
//
// S3 credentials come from HEARTH_S3_ACCESS_KEY / HEARTH_S3_SECRET_KEY,
// the AWS_* variables, or the instance role (IAM / IRSA).
func openBlobs(u string) (blobStore, error) {
	if !strings.HasPrefix(u, "s3://") {
		dir := strings.TrimPrefix(u, "file://")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		return &localBlobs{dir: dir}, nil
	}
	p, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	q := p.Query()
	endpoint := q.Get("endpoint")
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	secure := q.Get("insecure") == ""
	if h, ok := strings.CutPrefix(endpoint, "http://"); ok {
		endpoint, secure = h, false
	}
	endpoint = strings.TrimPrefix(endpoint, "https://")
	creds := credentials.NewChainCredentials([]credentials.Provider{
		&credentials.Static{Value: credentials.Value{
			AccessKeyID: os.Getenv("HEARTH_S3_ACCESS_KEY"), SecretAccessKey: os.Getenv("HEARTH_S3_SECRET_KEY"),
			SignerType: credentials.SignatureV4}},
		&credentials.EnvAWS{},
		&credentials.IAM{},
	})
	lookup := minio.BucketLookupAuto
	if q.Get("path_style") != "" {
		lookup = minio.BucketLookupPath
	}
	c, err := minio.New(endpoint, &minio.Options{Creds: creds, Secure: secure, Region: q.Get("region"), BucketLookup: lookup})
	if err != nil {
		return nil, err
	}
	b := &s3Blobs{c: c, bucket: p.Host, prefix: strings.Trim(p.Path, "/")}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ok, err := c.BucketExists(ctx, b.bucket)
	if err != nil {
		return nil, fmt.Errorf("s3 bucket %s at %s: %w", b.bucket, endpoint, err)
	}
	if !ok {
		return nil, fmt.Errorf("s3 bucket %s does not exist at %s", b.bucket, endpoint)
	}
	return b, nil
}

// localBlobs stores each blob as a file (default, single machine).
type localBlobs struct{ dir string }

func (l *localBlobs) String() string { return "file://" + l.dir }

func (l *localBlobs) path(name string) string { return filepath.Join(l.dir, filepath.FromSlash(name)) }

// put writes durably: temp file, fsync, rename.
func (l *localBlobs) put(name string, data []byte) error {
	p := l.path(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (l *localBlobs) get(name string) ([]byte, error) { return os.ReadFile(l.path(name)) }

func (l *localBlobs) del(name string) error {
	if err := os.Remove(l.path(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (l *localBlobs) list(fn func(string, time.Time, int64) error) error {
	return filepath.WalkDir(l.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(l.dir, p)
		return fn(filepath.ToSlash(rel), info.ModTime(), info.Size())
	})
}

// s3Blobs stores blobs in any S3-compatible service (AWS, MinIO, BOS, OSS,
// COS, Ceph RGW), so several server instances can share them.
type s3Blobs struct {
	c      *minio.Client
	bucket string
	prefix string
}

func (b *s3Blobs) String() string { return "s3://" + b.bucket + "/" + b.prefix }

func (b *s3Blobs) key(name string) string {
	if b.prefix == "" {
		return name
	}
	return b.prefix + "/" + name
}

const blobTimeout = 60 * time.Second

func (b *s3Blobs) put(name string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), blobTimeout)
	defer cancel()
	_, err := b.c.PutObject(ctx, b.bucket, b.key(name), bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream", DisableMultipart: true})
	return err
}

func (b *s3Blobs) get(name string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), blobTimeout)
	defer cancel()
	o, err := b.c.GetObject(ctx, b.bucket, b.key(name), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer o.Close()
	data, err := io.ReadAll(o)
	if err != nil && minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return nil, fmt.Errorf("%w: %s", os.ErrNotExist, name)
	}
	return data, err
}

func (b *s3Blobs) del(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), blobTimeout)
	defer cancel()
	err := b.c.RemoveObject(ctx, b.bucket, b.key(name), minio.RemoveObjectOptions{})
	if err != nil && minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return nil
	}
	return err
}

func (b *s3Blobs) list(fn func(string, time.Time, int64) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prefix := ""
	if b.prefix != "" {
		prefix = b.prefix + "/"
	}
	for o := range b.c.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return o.Err
		}
		if err := fn(strings.TrimPrefix(o.Key, prefix), o.LastModified, o.Size); err != nil {
			return err
		}
	}
	return nil
}
