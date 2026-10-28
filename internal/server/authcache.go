package server

import (
	"sync"
	"time"
)

// authCache keeps validated tokens in memory so that authenticated reads do
// not hit the database, and so token expiry / device last-seen are written
// at most once per touchEvery instead of on every request.
type authCache struct {
	mu      sync.RWMutex
	entries map[string]*authEntry
}

type authEntry struct {
	uid     int64
	did     string
	checked time.Time // last validation against the database
	touched time.Time // last expiry/last_seen write
	hash    []byte
}

const (
	revalidateEvery = 30 * time.Second // upper bound for revocation by other servers/tools
	touchEvery      = 5 * time.Minute
	maxAuthEntries  = 100_000
)

func newAuthCache() *authCache { return &authCache{entries: map[string]*authEntry{}} }

func (c *authCache) check(s *Server, hash []byte) (int64, string, error) {
	now := s.cfg.Now()
	key := string(hash)
	c.mu.RLock()
	e := c.entries[key]
	c.mu.RUnlock()
	if e == nil || now.Sub(e.checked) > revalidateEvery {
		uid, did, err := s.st.lookupToken(hash, now.Unix())
		if err != nil {
			c.mu.Lock()
			delete(c.entries, key)
			c.mu.Unlock()
			return 0, "", err
		}
		c.mu.Lock()
		if len(c.entries) >= maxAuthEntries {
			c.entries = map[string]*authEntry{} // crude bound; entries are cheap to rebuild
		}
		if e = c.entries[key]; e == nil {
			e = &authEntry{hash: hash}
			c.entries[key] = e
		}
		e.uid, e.did, e.checked = uid, did, now
		c.mu.Unlock()
	}
	c.mu.Lock()
	needTouch := now.Sub(e.touched) > touchEvery
	if needTouch {
		e.touched = now
	}
	uid, did := e.uid, e.did
	c.mu.Unlock()
	if needTouch {
		if err := s.st.touchToken(hash, did, now.Unix(), int64(s.cfg.TokenTTL.Seconds())); err != nil {
			return 0, "", err
		}
	}
	return uid, did, nil
}

// purgeUser drops cached tokens of a user (device revoked, account reset).
func (c *authCache) purgeUser(uid int64) {
	c.mu.Lock()
	for k, e := range c.entries {
		if e.uid == uid {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
}

// retouchDevice makes the next request of a device write last_seen again
// (it signed off; coming back must mark it online at once).
func (c *authCache) retouchDevice(did string) {
	c.mu.Lock()
	for _, e := range c.entries {
		if e.did == did {
			e.touched = time.Time{}
		}
	}
	c.mu.Unlock()
}
