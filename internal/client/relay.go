package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/relay"
)

// The relay (package relay): a machine that nobody can connect to still
// accepts SSH connections from the user's other machines, through the
// Hearth server, with no listening port reachable from outside.

// RelayOn reports whether this machine joins the relay: SSH is on and
// [ssh] relay is not false.
func RelayOn(home string) bool {
	return SSHOn(home) && !RelayDisabled(home)
}

// ErrNotInRelay: the machine has no relay connection to the server now.
var ErrNotInRelay = errors.New("that machine is not connected to the relay (is its hearth daemon running, and hearth up to date?)")

// wsURL turns the server's https URL into the WebSocket URL of a relay route.
func (e *Engine) wsURL(path string, q url.Values) string {
	u := e.API.Base
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	u += path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (e *Engine) relayDial(ctx context.Context, path string, q url.Values) (*websocket.Conn, error) {
	hdr := http.Header{"Authorization": {"Bearer " + e.API.Token}}
	c, resp, err := websocket.Dial(ctx, e.wsURL(path, q), &websocket.DialOptions{HTTPClient: e.API.HC, HTTPHeader: hdr})
	if err == nil {
		return c, nil
	}
	if resp != nil {
		var body struct {
			Error string `json:"error"`
		}
		if resp.Body != nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = json.Unmarshal(b, &body)
		}
		if resp.StatusCode == http.StatusNotFound && strings.Contains(body.Error, "not connected to the relay") {
			return nil, ErrNotInRelay
		}
		if body.Error != "" {
			return nil, &HTTPError{Status: resp.StatusCode, Body: api.Error{Error: body.Error}}
		}
		return nil, &HTTPError{Status: resp.StatusCode, Body: api.Error{Error: http.StatusText(resp.StatusCode)}}
	}
	return nil, netError(e.API.Base, err)
}

// RelayDevices lists the user's machines that hold a relay connection now.
func (e *Engine) RelayDevices() ([]string, error) {
	var r struct {
		Devices []string `json:"devices"`
	}
	_, err := e.API.do("GET", "/v1/relay/status", nil, nil, nil, &r)
	return r.Devices, err
}

// RelayDisabled reports [ssh] relay = false: this machine neither joins the
// relay nor writes ProxyCommand lines that use it.
func RelayDisabled(home string) bool {
	c, err := UserConfig(home)
	return err == nil && c.SSH != nil && c.SSH.Relay != nil && !*c.SSH.Relay
}

// DialRelay connects to the sshd of one of the user's machines through the
// server. The returned connection speaks SSH to that machine.
func (e *Engine) DialRelay(ctx context.Context, deviceID string) (net.Conn, error) {
	c, err := e.relayDial(ctx, "/v1/relay/connect", url.Values{"device": {deviceID}})
	if err != nil {
		return nil, err
	}
	return websocket.NetConn(context.Background(), c, websocket.MessageBinary), nil
}

// RelayBackend gives the agent the connection to serve: by default the
// sshd Hearth runs on this machine.
type RelayBackend func(ctx context.Context) (net.Conn, error)

func (e *Engine) sshdBackend(ctx context.Context) (net.Conn, error) {
	st := SSHServerStatus(e.Home)
	if st.Port == 0 || !st.Running {
		return nil, errors.New("sshd is not running on that machine (hearth ssh status)")
	}
	d := net.Dialer{Timeout: 3 * time.Second}
	return d.DialContext(ctx, "tcp", net.JoinHostPort(st.Address, strconv.Itoa(st.Port)))
}

// RunRelayAgent keeps this machine reachable through the relay until ctx
// ends: one outbound WebSocket to the server, reconnected with a growing
// pause; for each connection another machine asks for, a second outbound
// WebSocket joined to backend. A nil backend is the local sshd. logf gets
// one line per change of state, not per connection.
func (e *Engine) RunRelayAgent(ctx context.Context, backend RelayBackend, logf func(string, ...any)) {
	if backend == nil {
		backend = e.sshdBackend
	}
	pause, lastMsg := time.Second, ""
	for ctx.Err() == nil {
		started := time.Now()
		err := e.relayControl(ctx, backend, func() {
			if lastMsg != "up" {
				logf("relay: connected, this machine accepts connections from your other machines through the server")
				lastMsg = "up"
			}
		})
		var he *HTTPError
		if errors.As(err, &he) && he.Status == http.StatusGone {
			return // the machine was removed from the account
		}
		if err != nil && ctx.Err() == nil {
			if msg := "relay: " + err.Error(); msg != lastMsg {
				logf("%s", msg)
				lastMsg = msg
			}
		} else if ctx.Err() == nil && lastMsg == "up" {
			logf("relay: connection to the server ended, reconnecting")
			lastMsg = "down"
		}
		if time.Since(started) > time.Minute {
			pause = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		if pause *= 2; pause > 30*time.Second {
			pause = 30 * time.Second
		}
	}
}

// relayControl is one run of the control connection.
func (e *Engine) relayControl(ctx context.Context, backend RelayBackend, up func()) error {
	c, err := e.relayDial(ctx, "/v1/relay/agent", nil)
	if err != nil {
		return err
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 16)
	up()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		var m struct {
			Op string `json:"op"`
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &m) != nil || m.Op != "open" || m.ID == "" {
			continue
		}
		go e.relayServe(ctx, m.ID, backend)
	}
}

// relayServe answers one request: connect to the backend, open the
// connection's WebSocket and join them. When the backend is not there the
// other machine is told why instead of waiting.
func (e *Engine) relayServe(ctx context.Context, id string, backend RelayBackend) {
	be, berr := backend(ctx)
	c, err := e.relayDial(ctx, "/v1/relay/accept", url.Values{"id": {id}})
	if err != nil {
		if be != nil {
			be.Close()
		}
		return
	}
	if berr != nil {
		msg := berr.Error()
		if len(msg) > 120 {
			msg = msg[:120]
		}
		c.Close(websocket.StatusInternalError, msg)
		return
	}
	relay.Bridge(websocket.NetConn(context.Background(), c, websocket.MessageBinary), be)
}

// RelayReason is the sentence a relay connection was closed with ("the
// machine did not answer", "sshd is not running on that machine"), or the
// error itself.
func RelayReason(err error) error {
	var ce websocket.CloseError
	if errors.As(err, &ce) && ce.Reason != "" {
		return errors.New(ce.Reason)
	}
	return err
}
