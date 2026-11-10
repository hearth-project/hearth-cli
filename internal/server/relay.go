package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// The relay (see package relay). Three routes, all behind a device login:
//
//	GET /v1/relay/agent            a machine's daemon holds this WebSocket open;
//	                               the server tells it which connections to open
//	GET /v1/relay/connect?device=  another machine of the same user asks to be joined
//	                               to that machine; the server tells the agent
//	GET /v1/relay/accept?id=       the agent's answer: one WebSocket for that connection
//
// State is in memory, per server instance: with several instances behind a
// load balancer, the agent and the client must reach the same one.

const (
	maxRelayPerUser = 64
	relayPingEvery  = 25 * time.Second
	relayAcceptWait = 15 * time.Second
)

type relayHub struct {
	mu      sync.Mutex
	agents  map[string]*relayAgent // device id
	waiting map[string]*relayWait  // connection id
	users   map[int64]int          // joined connections per user
}

type relayAgent struct {
	uid  int64
	open chan string // connection ids to announce
	done chan struct{}
}

type relayWait struct {
	device string
	conn   chan *websocket.Conn
	done   chan struct{} // closed when the connection is over
}

func newRelayHub() *relayHub {
	return &relayHub{agents: map[string]*relayAgent{}, waiting: map[string]*relayWait{}, users: map[int64]int{}}
}

// RelayAgents is how many machines hold a relay connection now.
func (s *Server) RelayAgents() int {
	s.relay.mu.Lock()
	defer s.relay.mu.Unlock()
	return len(s.relay.agents)
}

// relayConnected reports whether the device holds a relay connection now.
func (s *Server) relayConnected(device string) bool {
	if device == "" || s.relay == nil {
		return false
	}
	s.relay.mu.Lock()
	defer s.relay.mu.Unlock()
	_, ok := s.relay.agents[device]
	return ok
}

// handleRelayStatus lists the caller's machines that hold a relay
// connection now: {"devices": ["<device id>", ...]}.
func (s *Server) handleRelayStatus(w http.ResponseWriter, r *http.Request) {
	uid, ids := userOf(r), []string{}
	s.relay.mu.Lock()
	for dev, ag := range s.relay.agents {
		if ag.uid == uid {
			ids = append(ids, dev)
		}
	}
	s.relay.mu.Unlock()
	sort.Strings(ids)
	writeJSON(w, http.StatusOK, map[string][]string{"devices": ids})
}

func (s *Server) handleRelayAgent(w http.ResponseWriter, r *http.Request) {
	dev, uid := deviceOf(r), userOf(r)
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	th := tokenHash(tok)
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 16)
	ag := &relayAgent{uid: uid, open: make(chan string, 16), done: make(chan struct{})}
	h := s.relay
	h.mu.Lock()
	if old := h.agents[dev]; old != nil {
		close(old.done) // a newer connection of the same machine replaces it
	}
	h.agents[dev] = ag
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.agents[dev] == ag {
			delete(h.agents, dev)
		}
		h.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // reading answers pings and sees the close
		defer cancel()
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}()
	tick := time.NewTicker(relayPingEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			c.Close(websocket.StatusNormalClosure, "")
			return
		case <-ag.done:
			c.Close(websocket.StatusNormalClosure, "replaced by a newer connection")
			return
		case id := <-ag.open:
			msg, _ := json.Marshal(map[string]string{"op": "open", "id": id})
			wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			wcancel()
			if err != nil {
				return
			}
		case <-tick.C:
			if _, _, err := s.auth.check(s, th); err != nil { // logged out, removed or disabled meanwhile
				c.Close(websocket.StatusPolicyViolation, "the login of this machine ended")
				return
			}
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) handleRelayConnect(w http.ResponseWriter, r *http.Request) {
	target, uid := r.URL.Query().Get("device"), userOf(r)
	h := s.relay
	id := hex.EncodeToString(crypto.RandomBytes(12))
	h.mu.Lock()
	ag := h.agents[target]
	switch {
	case ag == nil || ag.uid != uid: // another user's machine looks the same as none
		h.mu.Unlock()
		writeErr(w, http.StatusNotFound, "that machine is not connected to the relay (is its hearth daemon running?)")
		return
	case h.users[uid] >= maxRelayPerUser:
		h.mu.Unlock()
		writeErr(w, http.StatusTooManyRequests, "too many relayed connections open for this account")
		return
	}
	h.users[uid]++
	wt := &relayWait{device: target, conn: make(chan *websocket.Conn, 1), done: make(chan struct{})}
	h.waiting[id] = wt
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.users[uid]--
		delete(h.waiting, id)
		h.mu.Unlock()
		close(wt.done)
	}()

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	select {
	case ag.open <- id:
	case <-time.After(5 * time.Second):
		c.Close(websocket.StatusTryAgainLater, "the machine's relay is busy")
		return
	}
	select {
	case ac := <-wt.conn:
		joinRelay(c, ac)
	case <-time.After(relayAcceptWait):
		c.Close(websocket.StatusTryAgainLater, "the machine did not answer")
	case <-ag.done:
		c.Close(websocket.StatusGoingAway, "the machine left the relay")
	}
}

// joinRelay copies between the asking machine's WebSocket and the agent's
// until either ends. When the agent closes with a reason ("sshd is not
// running on that machine"), the asking machine gets that reason too.
func joinRelay(client, agent *websocket.Conn) {
	cn := websocket.NetConn(context.Background(), client, websocket.MessageBinary)
	an := websocket.NetConn(context.Background(), agent, websocket.MessageBinary)
	go func() {
		io.Copy(an, cn)
		an.Close()
	}()
	_, err := io.Copy(cn, an)
	var ce websocket.CloseError
	if errors.As(err, &ce) && ce.Reason != "" {
		client.Close(ce.Code, ce.Reason)
	} else {
		client.Close(websocket.StatusNormalClosure, "")
	}
	an.Close()
}

func (s *Server) handleRelayAccept(w http.ResponseWriter, r *http.Request) {
	id, dev := r.URL.Query().Get("id"), deviceOf(r)
	h := s.relay
	h.mu.Lock()
	wt := h.waiting[id]
	if wt == nil || wt.device != dev {
		h.mu.Unlock()
		writeErr(w, http.StatusNotFound, "no such pending connection")
		return
	}
	delete(h.waiting, id) // answered once
	h.mu.Unlock()
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	select {
	case wt.conn <- c:
		<-wt.done // the connect handler owns both ends until it is over
	default:
		c.Close(websocket.StatusPolicyViolation, "already answered")
	}
}
