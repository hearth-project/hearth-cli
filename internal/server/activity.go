package server

import (
	"sync"
	"time"
)

// activity counts uploads per user and day and remembers each device's
// client version in memory; flush writes them every activityFlush, so the
// hot append path only takes a mutex.

const activityFlush = time.Minute

type dayUser struct {
	day string
	uid int64
}

type dayCount struct{ chunks, bytes int64 }

// deviceSeen is what the panel shows about a device's last request.
type deviceSeen struct{ version, ip string }

type activity struct {
	mu       sync.Mutex
	counts   map[dayUser]dayCount
	versions map[string]deviceSeen // device -> version and address, changed since the last flush
	seen     map[string]deviceSeen // device -> what is already stored
	stop     chan struct{}
	done     chan struct{}
}

func newActivity() *activity {
	return &activity{counts: map[dayUser]dayCount{}, versions: map[string]deviceSeen{}, seen: map[string]deviceSeen{},
		stop: make(chan struct{}), done: make(chan struct{})}
}

func (a *activity) upload(now time.Time, uid, bytes int64) {
	k := dayUser{now.UTC().Format(time.DateOnly), uid}
	a.mu.Lock()
	c := a.counts[k]
	c.chunks++
	c.bytes += bytes
	a.counts[k] = c
	a.mu.Unlock()
}

// device records the client version (empty from clients older than the
// header: kept as stored) and address of a device's request.
func (a *activity) device(device, version, ip string) {
	if len(version) > 64 {
		version = ""
	}
	if len(ip) > 64 {
		ip = ip[:64]
	}
	a.mu.Lock()
	cur := a.seen[device]
	if p, ok := a.versions[device]; ok {
		cur = p
	}
	if version == "" {
		version = cur.version
	}
	if n := (deviceSeen{version, ip}); n != cur {
		a.versions[device] = n
	}
	a.mu.Unlock()
}

// flush writes the counters (added to what is stored) and new versions.
func (s *Server) flushActivity() error {
	a := s.activity
	a.mu.Lock()
	counts, versions := a.counts, a.versions
	a.counts, a.versions = map[dayUser]dayCount{}, map[string]deviceSeen{}
	a.mu.Unlock()
	if len(counts)+len(versions) == 0 {
		return nil
	}
	tx, err := s.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, c := range counts {
		if _, err := tx.Exec(`INSERT INTO activity_daily(day,user_id,chunks,bytes) VALUES(?,?,?,?)
			ON CONFLICT(day,user_id) DO UPDATE SET chunks=activity_daily.chunks+excluded.chunks, bytes=activity_daily.bytes+excluded.bytes`,
			k.day, k.uid, c.chunks, c.bytes); err != nil {
			return err
		}
	}
	for d, v := range versions {
		// An unknown version (old client) keeps what is stored.
		if _, err := tx.Exec(`UPDATE devices SET client_version=CASE WHEN ?='' THEN client_version ELSE ? END, last_ip=? WHERE id=?`,
			v.version, v.version, v.ip, d); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.mu.Lock()
	for d, v := range versions {
		a.seen[d] = v
	}
	a.mu.Unlock()
	return nil
}

func (s *Server) activityLoop() {
	a := s.activity
	defer close(a.done)
	t := time.NewTicker(activityFlush)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			if err := s.flushActivity(); err != nil {
				s.cfg.Logger.Printf("activity: %v", err)
			}
			return
		case <-t.C:
			if err := s.flushActivity(); err != nil {
				s.cfg.Logger.Printf("activity: %v", err)
			}
		}
	}
}
