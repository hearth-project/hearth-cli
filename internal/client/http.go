package client

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"

	"github.com/hearth-project/hearth-cli/internal/api"
)

// HTTPError is a non-2xx server response.
type HTTPError struct {
	Status int
	Body   api.Error
}

func (e *HTTPError) Error() string { return fmt.Sprintf("server: %d %s", e.Status, e.Body.Error) }

// StatusOf returns the HTTP status of err, or 0.
func StatusOf(err error) int {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

// API is a thin typed client for the server's HTTP API.
type API struct {
	Base  string
	Token string
	HC    *http.Client
}

// NewAPI builds a client. hc may be nil for the default HTTP client.
func NewAPI(base, token string, hc *http.Client) *API {
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	return &API{Base: strings.TrimRight(base, "/"), Token: token, HC: hc}
}

// CAFile is where a private server CA (e.g. a self-signed server
// certificate) is stored inside the client home.
func CAFile(home string) string { return filepath.Join(home, "ca.pem") }

// DefaultHome is the state directory used without a workspace (~/.hearth),
// where the installer stores the server CA.
func DefaultHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ".hearth"
	}
	return filepath.Join(h, ".hearth")
}

// caFiles lists the CA files a home trusts: its own ca.pem, the default
// home's (the installer writes it there before a workspace exists) and
// $HEARTH_CA_CERT.
func caFiles(home string) []string {
	out := []string{CAFile(home)}
	if d := CAFile(DefaultHome()); filepath.Clean(d) != filepath.Clean(out[0]) {
		out = append(out, d)
	}
	if v := os.Getenv("HEARTH_CA_CERT"); v != "" {
		out = append(out, v)
	}
	return out
}

// HTTPClient returns a client that trusts the system roots plus the
// private CAs of caFiles.
func HTTPClient(home string) (*http.Client, error) {
	var pemData []byte
	for _, p := range caFiles(home) {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		pemData = append(append(pemData, b...), '\n')
	}
	useRememberedProxy(home)
	autoProxyFor(home)
	if len(pemData) == 0 {
		return &http.Client{Timeout: 60 * time.Second, Transport: fastTransport()}, nil
	}
	return httpClientWithCA(pemData)
}

// fastTransport fails in seconds when the server cannot be reached at all
// (no route, a proxy missing in this shell) instead of hanging for a minute.
func fastTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 15 * time.Second
	// The proxy is looked up per request: http.ProxyFromEnvironment reads
	// the environment once per process, before a remembered proxy is set.
	tr.Proxy = func(r *http.Request) (*url.URL, error) {
		return httpproxy.FromEnvironment().ProxyFunc()(r.URL)
	}
	return tr
}

func netStatePath(home string) string { return filepath.Join(home, "net.json") }

type netState struct {
	HTTPSProxy string `json:"https_proxy,omitempty"`
	HTTPProxy  string `json:"http_proxy,omitempty"`
	NoProxy    string `json:"no_proxy,omitempty"`
}

func proxyFromEnv() netState {
	get := func(k string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return os.Getenv(strings.ToUpper(k))
	}
	return netState{HTTPSProxy: get("https_proxy"), HTTPProxy: get("http_proxy"), NoProxy: get("no_proxy")}
}

// RememberProxy records the proxy this process reached the server with
// (the daemon, started from a shell that has it), so commands run where
// the proxy is not set (hearth ssh <machine> -- hearth ..., cron) use it.
func RememberProxy(home string) {
	cur := proxyFromEnv()
	var old netState
	_ = loadJSON(netStatePath(home), &old)
	if cur != old {
		_ = saveJSON(netStatePath(home), cur)
	}
}

// useRememberedProxy sets the remembered proxy for this process when its
// environment has none.
func useRememberedProxy(home string) {
	if p := proxyFromEnv(); p.HTTPSProxy != "" || p.HTTPProxy != "" {
		return
	}
	var st netState
	if loadJSON(netStatePath(home), &st) != nil {
		return
	}
	for k, v := range map[string]string{"https_proxy": st.HTTPSProxy, "http_proxy": st.HTTPProxy, "no_proxy": st.NoProxy} {
		if v != "" {
			os.Setenv(k, v)
		}
	}
}

// adoptCA copies the CA this home relies on from elsewhere (default home,
// $HEARTH_CA_CERT) into the home, so the workspace stays self-contained
// even if ~/.hearth is cleaned up. No-op when the home has its own.
func adoptCA(home string) error {
	if _, err := os.Stat(CAFile(home)); err == nil {
		return nil
	}
	for _, p := range caFiles(home)[1:] {
		if b, err := os.ReadFile(p); err == nil {
			if _, err := httpClientWithCA(b); err == nil {
				return writeFileAtomic(CAFile(home), b, 0o600)
			}
		}
	}
	return nil
}

func httpClientWithCA(pemData []byte) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, errors.New("CA file contains no PEM certificates")
	}
	tr := fastTransport()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Timeout: 60 * time.Second, Transport: tr}, nil
}

// InstallCA validates a PEM file and copies it to home/ca.pem.
func InstallCA(home, src string) error {
	pemData, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if _, err := httpClientWithCA(pemData); err != nil {
		return err
	}
	return writeFileAtomic(CAFile(home), pemData, 0o600)
}

// Version is this client's build, sent with authenticated requests so the
// server admin sees which versions are in use (set by main).
var Version string

func (a *API) do(method, path string, in any, raw []byte, hdr map[string]string, out any) (http.Header, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	} else if raw != nil {
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.Base+path, body)
	if err != nil {
		return nil, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	} else if raw != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
		if Version != "" {
			req.Header.Set(api.HeaderClientVersion, Version)
		}
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := a.HC.Do(req)
	if err != nil {
		return nil, netError(a.Base, err)
	}
	defer resp.Body.Close()
	limit := int64(api.MaxChunkBytes + (1 << 20))
	switch p := strings.SplitN(path, "?", 2)[0]; {
	case strings.HasSuffix(p, "/content"):
		limit = 4 << 30 // whole-session stream
	case p == "/v1/library":
		limit = api.MaxLibraryBytes + (1 << 20) // a library bundle is larger than a chunk
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		// Cut off, it would fail later as a corrupt body (unexpected EOF).
		return nil, fmt.Errorf("%s %s: response larger than %d bytes", method, path, limit)
	}
	if resp.StatusCode/100 != 2 {
		he := &HTTPError{Status: resp.StatusCode}
		_ = json.Unmarshal(data, &he.Body)
		return resp.Header, he
	}
	switch o := out.(type) {
	case nil:
	case *[]byte:
		*o = data
	default:
		if len(data) == 0 {
			break // 204 No Content
		}
		if err := json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return resp.Header, nil
}

// getCounted is a GET whose body is read with onRead called as bytes
// arrive (progress of a large download).
func (a *API) getCounted(path string, onRead func(int64)) ([]byte, error) {
	req, err := http.NewRequest("GET", a.Base+path, nil)
	if err != nil {
		return nil, err
	}
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	resp, err := a.HC.Do(req)
	if err != nil {
		return nil, netError(a.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		he := &HTTPError{Status: resp.StatusCode}
		_ = json.Unmarshal(data, &he.Body)
		return nil, he
	}
	var buf bytes.Buffer
	if resp.ContentLength > 0 {
		buf.Grow(int(resp.ContentLength))
	}
	chunk := make([]byte, 256<<10)
	r := io.LimitReader(resp.Body, 4<<30)
	for {
		n, err := r.Read(chunk)
		buf.Write(chunk[:n])
		if n > 0 {
			onRead(int64(buf.Len()))
		}
		if err == io.EOF {
			return buf.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func sp(sid string) string { return "/v1/sessions/" + url.PathEscape(sid) }

func (a *API) Prelogin(email string) (api.PreloginResponse, error) {
	var r api.PreloginResponse
	_, err := a.do("POST", "/v1/auth/prelogin", api.PreloginRequest{Email: email}, nil, nil, &r)
	return r, err
}

func (a *API) Register(req api.RegisterRequest) error {
	_, err := a.do("POST", "/v1/auth/register", req, nil, nil, nil)
	return err
}

func (a *API) Login(req api.LoginRequest) (api.LoginResponse, error) {
	var r api.LoginResponse
	_, err := a.do("POST", "/v1/auth/login", req, nil, nil, &r)
	return r, err
}

func (a *API) EnableIdentity(req api.IdentityEnableRequest) error {
	_, err := a.do("POST", "/v1/auth/identity/enable", req, nil, nil, nil)
	return err
}

func (a *API) DisableIdentity() error {
	_, err := a.do("DELETE", "/v1/auth/identity", nil, nil, nil, nil)
	return err
}

func (a *API) IdentityStatus() (api.IdentityStatusResponse, error) {
	var r api.IdentityStatusResponse
	_, err := a.do("GET", "/v1/auth/identity", nil, nil, nil, &r)
	return r, err
}

func (a *API) IdentityLogin(req api.IdentityLoginRequest) (api.IdentityLoginResponse, error) {
	var r api.IdentityLoginResponse
	_, err := a.do("POST", "/v1/auth/identity/login", req, nil, nil, &r)
	return r, err
}

func (a *API) Devices() ([]api.Device, error) {
	var r []api.Device
	_, err := a.do("GET", "/v1/devices", nil, nil, nil, &r)
	return r, err
}

// Heartbeat tells the server this device is online (the daemon, every scan).
func (a *API) Heartbeat() error { return a.HeartbeatReport(nil, nil) }

// HeartbeatReport publishes non-secret machine and agent readiness data so
// another logged-in machine can discover and restore this one. It is bounded
// and never includes credentials, sessions or file contents.
func (a *API) HeartbeatReport(machine, health map[string]any) error {
	_, err := a.do("POST", "/v1/devices/self/heartbeat", api.DeviceHeartbeat{Machine: machine, Health: health}, nil, nil, nil)
	return err
}

// SignOff marks this device offline (graceful daemon stop): other machines
// then restore its conversations by default again.
func (a *API) SignOff() error {
	_, err := a.do("POST", "/v1/devices/self/offline", struct{}{}, nil, nil, nil)
	return err
}

// OnlineDevices returns the user's other devices that are online now, by
// id (empty on servers that do not report it).
func (e *Engine) OnlineDevices() map[string]string {
	out := map[string]string{}
	ds, err := e.API.Devices()
	if err != nil {
		return out
	}
	for _, d := range ds {
		if d.Online && d.ID != e.Cfg.DeviceID {
			out[d.ID] = d.Name
		}
	}
	return out
}

// Usage returns the account's storage use and quota.
func (a *API) Usage() (api.AccountUsage, error) {
	var r api.AccountUsage
	_, err := a.do("GET", "/v1/account/usage", nil, nil, nil, &r)
	return r, err
}

func (a *API) RevokeDevice(id string) error {
	_, err := a.do("DELETE", "/v1/devices/"+url.PathEscape(id), nil, nil, nil, nil)
	return err
}

func (a *API) Sessions() ([]api.Session, error) {
	var r []api.Session
	_, err := a.do("GET", "/v1/sessions", nil, nil, nil, &r)
	return r, err
}

func (a *API) Session(sid string) (api.Session, error) {
	var r api.Session
	_, err := a.do("GET", sp(sid), nil, nil, nil, &r)
	return r, err
}

func (a *API) PutSession(sid string, encMeta []byte) (api.Session, error) {
	var r api.Session
	_, err := a.do("PUT", sp(sid), api.PutSessionRequest{EncMeta: encMeta}, nil, nil, &r)
	return r, err
}

func (a *API) DeleteSession(sid string) error {
	_, err := a.do("DELETE", sp(sid), nil, nil, nil, nil)
	return err
}

func (a *API) post(sid, action string, in any) (api.Session, error) {
	var r api.Session
	if in == nil {
		in = struct{}{}
	}
	_, err := a.do("POST", sp(sid)+"/"+action, in, nil, nil, &r)
	return r, err
}

func (a *API) Touch(sid string) (api.Session, error)  { return a.post(sid, "touch", nil) }
func (a *API) Expire(sid string) (api.Session, error) { return a.post(sid, "expire", nil) }
func (a *API) Reset(sid string) (api.Session, error)  { return a.post(sid, "reset", nil) }
func (a *API) Pin(sid string, pinned bool) (api.Session, error) {
	return a.post(sid, "pin", api.PinRequest{Pinned: pinned})
}

func (a *API) Revive(sid string) error {
	_, err := a.do("POST", sp(sid)+"/revive", struct{}{}, nil, nil, nil)
	return err
}

func (a *API) Lease(sid string, ttl time.Duration, force bool) (api.Session, error) {
	return a.post(sid, "lease", api.LeaseRequest{TTLSeconds: int64(ttl.Seconds()), Force: force})
}

func (a *API) ReleaseLease(sid string) error {
	_, err := a.do("DELETE", sp(sid)+"/lease", nil, nil, nil, nil)
	return err
}

func (a *API) Chunks(sid string) ([]api.Chunk, error) {
	var r []api.Chunk
	_, err := a.do("GET", sp(sid)+"/chunks", nil, nil, nil, &r)
	return r, err
}

func (a *API) AppendChunk(sid string, gen, offset int64, plainLen int, ct []byte) (api.Session, error) {
	var r api.Session
	_, err := a.do("PUT", fmt.Sprintf("%s/chunks/%d", sp(sid), offset), nil, ct, map[string]string{
		api.HeaderGeneration:  strconv.FormatInt(gen, 10),
		api.HeaderPlainLength: strconv.Itoa(plainLen),
	}, &r)
	return r, err
}

func (a *API) GetChunk(sid string, gen, offset int64) ([]byte, error) {
	var b []byte
	_, err := a.do("GET", fmt.Sprintf("%s/chunks/%d?generation=%d", sp(sid), offset, gen), nil, nil, nil, &b)
	return b, err
}

// NetError says in plain words why the server could not be reached and
// what to try; the original error stays wrapped.
type NetError struct {
	Server, Hint string
	Err          error
}

func (e *NetError) Error() string {
	return fmt.Sprintf("cannot reach the hearth server %s: %s (%v)", e.Server, e.Hint, e.Err)
}

func (e *NetError) Unwrap() error { return e.Err }

func netError(server string, err error) error {
	msg := err.Error()
	hint := ""
	switch {
	case strings.Contains(msg, "x509:") || strings.Contains(msg, "tls:"):
		hint = "its certificate is not trusted here; re-run the install command from the admin (it brings the certificate), or pass --ca-cert <file>"
	case strings.Contains(msg, "connection refused"):
		hint = "nothing answers on that port; is the server running?"
	case strings.Contains(msg, "no such host"):
		hint = "unknown host name; check the URL"
	case strings.Contains(msg, "Client.Timeout") || strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded"):
		hint = "timed out; check the network, VPN or proxy (HTTPS_PROXY / NO_PROXY)"
		if p := proxyFromEnv(); p.HTTPSProxy == "" && p.HTTPProxy == "" {
			hint = "timed out, and this shell has no proxy set; if this machine reaches the server through one, set https_proxy=http://<proxy>:<port> (the hearth daemon remembers it once it ran with it)"
		}
	case strings.Contains(msg, "network is unreachable") || strings.Contains(msg, "no route to host"):
		hint = "the network has no route there; check VPN or proxy"
	default:
		return err
	}
	return &NetError{Server: server, Hint: hint, Err: err}
}
