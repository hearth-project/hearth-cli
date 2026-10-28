package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Runtime settings changed from the admin panel. They live in server_kv
// (key "settings"), so every instance sees them within settingsRefresh;
// a setting never changed there keeps the value of the command-line flag.

const settingsRefresh = 10 * time.Second

// RegistrationApproval: anyone may sign up; the account works once an
// admin approves it.
const RegistrationApproval = "approval"

// Settings is the effective configuration shown in the panel.
type Settings struct {
	Registration     string `json:"registration"`  // invite | open | approval | closed
	DefaultQuota     int64  `json:"default_quota"` // bytes per account; 0 = unlimited
	ClientAutoUpdate bool   `json:"client_auto_update"`
}

// storedSettings: nil = not set in the panel, use the flag.
type storedSettings struct {
	Registration     *string `json:"registration,omitempty"`
	DefaultQuota     *int64  `json:"default_quota,omitempty"`
	ClientAutoUpdate *bool   `json:"client_auto_update,omitempty"`
}

type settingsCache struct {
	mu sync.Mutex
	v  storedSettings
	at time.Time
}

func validRegistration(m string) bool {
	switch m {
	case RegistrationInvite, RegistrationOpen, RegistrationApproval, RegistrationClosed:
		return true
	}
	return false
}

func (s *Server) stored() storedSettings {
	c := &s.settings
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < settingsRefresh && !c.at.IsZero() {
		return c.v
	}
	var raw []byte
	err := s.st.rdb.QueryRow(`SELECT value FROM server_kv WHERE key='settings'`).Scan(&raw)
	var v storedSettings
	if err == nil {
		_ = json.Unmarshal(raw, &v)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return c.v // keep the last good value on a read error
	}
	c.v, c.at = v, time.Now()
	return v
}

// Settings returns the effective settings.
func (s *Server) Settings() Settings {
	v := s.stored()
	out := Settings{Registration: s.cfg.Registration, DefaultQuota: s.cfg.DefaultQuota, ClientAutoUpdate: s.dist == nil || !s.dist.NoAutoUpdate}
	if v.Registration != nil && validRegistration(*v.Registration) {
		out.Registration = *v.Registration
	}
	if v.DefaultQuota != nil && *v.DefaultQuota >= 0 {
		out.DefaultQuota = *v.DefaultQuota
	}
	if v.ClientAutoUpdate != nil {
		out.ClientAutoUpdate = *v.ClientAutoUpdate
	}
	return out
}

func (s *Server) registration() string { return s.Settings().Registration }
func (s *Server) defaultQuota() int64  { return s.Settings().DefaultQuota }

// SetSettings stores the given settings (all of them).
func (s *Server) SetSettings(n Settings) error {
	if !validRegistration(n.Registration) {
		return fmt.Errorf("%w: registration must be invite, open, approval or closed", errBadRequest)
	}
	if n.DefaultQuota < 0 {
		return fmt.Errorf("%w: default quota must be >= 0 (0 = unlimited)", errBadRequest)
	}
	b, _ := json.Marshal(storedSettings{Registration: &n.Registration, DefaultQuota: &n.DefaultQuota, ClientAutoUpdate: &n.ClientAutoUpdate})
	if _, err := s.st.db.Exec(`INSERT INTO server_kv(key,value) VALUES('settings',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, b); err != nil {
		return err
	}
	s.settings.mu.Lock()
	s.settings.at = time.Time{}
	s.settings.mu.Unlock()
	s.quota.reset() // the default quota may have changed
	return nil
}
