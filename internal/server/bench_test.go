package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/crypto"
	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

// benchEnv is a server with one user, one device token and nSessions
// sessions of chunks chunks each, reached over real HTTP.
type benchEnv struct {
	url, token string
	sids       []string
	hc         *http.Client
}

var benchQuota int64

func newBench(b *testing.B, nSessions, chunks int) *benchEnv {
	b.Helper()
	srv, err := New(Config{DataDir: b.TempDir(), Database: servertest.Postgres(b), Blobs: servertest.S3(b), DefaultQuota: benchQuota, Registration: RegistrationOpen, AuthPerMin: 1 << 30, Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		b.Fatal(err)
	}
	hs := httptest.NewServer(srv)
	b.Cleanup(func() { hs.Close(); srv.Close() })
	e := &benchEnv{url: hs.URL, hc: &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 256}}}
	kdf := crypto.KDFParams{Time: 1, MemoryKiB: 19 * 1024, Threads: 1}
	auth := crypto.RandomBytes(32)
	e.must(b, "POST", "/v1/auth/register", api.RegisterRequest{Email: "b@x.io", KDF: kdf, Salt: crypto.RandomBytes(16), AuthKey: auth,
		EncVaultKey: []byte("x"), EncVaultKeyRecovery: []byte("y"), RecoveryAuth: crypto.RandomBytes(32)}, nil, nil)
	var lr api.LoginResponse
	e.must(b, "POST", "/v1/auth/login", api.LoginRequest{Email: "b@x.io", AuthKey: auth, DeviceName: "bench"}, nil, &lr)
	e.token = lr.Token
	chunk := bytes.Repeat([]byte("c"), 4096)
	for i := 0; i < nSessions; i++ {
		sid := hex.EncodeToString(crypto.RandomBytes(32))
		e.must(b, "PUT", "/v1/sessions/"+sid, api.PutSessionRequest{EncMeta: []byte("meta")}, nil, nil)
		for c := 0; c < chunks; c++ {
			e.putChunk(b, sid, int64(c*len(chunk)), chunk)
		}
		e.sids = append(e.sids, sid)
	}
	return e
}

func (e *benchEnv) do(method, path string, body io.Reader, hdr map[string]string) (*http.Response, error) {
	req, _ := http.NewRequest(method, e.url+path, body)
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return e.hc.Do(req)
}

func (e *benchEnv) must(b *testing.B, method, path string, in, _ any, out any) {
	var body io.Reader
	if in != nil {
		j, _ := json.Marshal(in)
		body = bytes.NewReader(j)
	}
	resp, err := e.do(method, path, body, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		b.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
	}
	if out != nil {
		json.Unmarshal(data, out)
	}
}

func (e *benchEnv) putChunk(b *testing.B, sid string, off int64, data []byte) {
	resp, err := e.do("PUT", fmt.Sprintf("/v1/sessions/%s/chunks/%d", sid, off), bytes.NewReader(data), map[string]string{
		api.HeaderGeneration: "1", api.HeaderPlainLength: strconv.Itoa(len(data))})
	if err != nil {
		b.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		b.Fatalf("append: %d", resp.StatusCode)
	}
}

func (e *benchEnv) get(b *testing.B, path string) {
	resp, err := e.do("GET", path, nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b.Fatalf("GET %s: %d", path, resp.StatusCode)
	}
}

// Read path the daemon and `session list` hit most: auth + list.
func BenchmarkListSessionsParallel(b *testing.B) {
	e := newBench(b, 200, 1)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			e.get(b, "/v1/sessions")
		}
	})
}

// Restore path: fetching every chunk of a session.
func BenchmarkDownloadSession(b *testing.B) {
	e := newBench(b, 1, 64)
	sid := e.sids[0]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.get(b, "/v1/sessions/"+sid+"/content?generation=1")
	}
}

// Live sync path: many devices appending concurrently to their sessions.
func BenchmarkAppendParallel(b *testing.B) {
	e := newBench(b, 0, 0)
	var n atomic.Int64
	chunk := bytes.Repeat([]byte("a"), 4096)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		sid := hex.EncodeToString(crypto.RandomBytes(32))
		e.must(b, "PUT", "/v1/sessions/"+sid, api.PutSessionRequest{EncMeta: []byte("m")}, nil, nil)
		var off int64
		for pb.Next() {
			e.putChunk(b, sid, off, chunk)
			off += int64(len(chunk))
			n.Add(1)
		}
	})
}

// Polling path: unchanged library returns 304.
func BenchmarkLibraryPoll(b *testing.B) {
	e := newBench(b, 0, 0)
	resp, _ := e.do("PUT", "/v1/library", bytes.NewReader([]byte("bundle")), map[string]string{
		api.HeaderLibraryVersion: "0", api.HeaderLibrarySHA256: string(bytes.Repeat([]byte("a"), 64))})
	resp.Body.Close()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := e.do("GET", "/v1/library", nil, map[string]string{api.HeaderLibraryVersion: "1"})
			if err != nil || resp.StatusCode != http.StatusNotModified {
				b.Fatalf("poll: %v %v", err, resp.StatusCode)
			}
			resp.Body.Close()
		}
	})
}

func skillBundle(body string) []byte {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	tw.WriteHeader(&tar.Header{Name: "SKILL.md", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(raw.Bytes())
	zw.Close()
	return gz.Bytes()
}

// Server-side team search over 5000 published skills (index built once).
func BenchmarkTeamSearchParallel(b *testing.B) {
	srv, err := New(Config{DataDir: b.TempDir(), Database: servertest.Postgres(b), Blobs: servertest.S3(b), Registration: RegistrationOpen, AuthPerMin: 1 << 30, Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		b.Fatal(err)
	}
	hs := httptest.NewServer(srv)
	b.Cleanup(func() { hs.Close(); srv.Close() })
	e := &benchEnv{url: hs.URL, hc: &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 256}}}
	kdf := crypto.KDFParams{Time: 1, MemoryKiB: 19 * 1024, Threads: 1}
	auth := crypto.RandomBytes(32)
	e.must(b, "POST", "/v1/auth/register", api.RegisterRequest{Email: "b@x.io", KDF: kdf, Salt: crypto.RandomBytes(16), AuthKey: auth,
		EncVaultKey: []byte("x"), EncVaultKeyRecovery: []byte("y"), RecoveryAuth: crypto.RandomBytes(32)}, nil, nil)
	var lr api.LoginResponse
	e.must(b, "POST", "/v1/auth/login", api.LoginRequest{Email: "b@x.io", AuthKey: auth, DeviceName: "bench"}, nil, &lr)
	e.token = lr.Token
	e.must(b, "POST", "/v1/teams", api.CreateTeamRequest{Name: "t"}, nil, nil)
	t, _ := srv.st.membership(1, "t", "member")
	words := []string{"benchmark", "profile", "nsys", "cuda", "kernel", "deploy", "压测", "吞吐", "显存", "调度", "日志", "icafe", "docker", "trace"}
	for i := 0; i < 5000; i++ {
		w1, w2 := words[i%len(words)], words[(i*7)%len(words)]
		body := fmt.Sprintf("---\ndescription: %s %s 工具\n---\n%s steps\n", w1, w2, w2)
		data := skillBundle(body)
		if _, _, err := srv.st.publish(t.ID, 1, "skill", fmt.Sprintf("s%d-%s", i, w1), w1+" "+w2+" 工具", fmt.Sprintf("%064d", i), data, packageText(data), 1); err != nil {
			b.Fatal(err)
		}
	}
	e.get(b, "/v1/teams/t/search?q=warmup") // build the index
	q := "/v1/teams/t/search?limit=5&q=" + url.QueryEscape("帮我压测一下吞吐 benchmark throughput")
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			e.get(b, q)
		}
	})
}

// Live sync path with a default quota: usage is checked from memory.
func BenchmarkAppendParallelWithQuota(b *testing.B) {
	benchQuota = 1 << 40
	defer func() { benchQuota = 0 }()
	BenchmarkAppendParallel(b)
}
