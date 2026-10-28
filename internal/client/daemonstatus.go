package client

import (
	"path/filepath"
	"time"
)

// DaemonStatus is the daemon's heartbeat, read by `hearth status`: when it
// last scanned, uploaded and synced the library, and its latest error.
type DaemonStatus struct {
	PID         int    `json:"pid"`
	Version     string `json:"version,omitempty"`
	Started     int64  `json:"started"`
	LastScan    int64  `json:"last_scan,omitempty"`
	LastUpload  int64  `json:"last_upload,omitempty"`
	UploadBytes int64  `json:"last_upload_bytes,omitempty"`
	UploadFiles int    `json:"last_upload_files,omitempty"`
	LastLibrary int64  `json:"last_library,omitempty"`
	LibraryOp   string `json:"library_op,omitempty"`
	Interval    string `json:"interval,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	ErrorAt     int64  `json:"error_at,omitempty"`
}

func daemonStatusPath(home string) string { return filepath.Join(home, "daemon-status.json") }

// LoadDaemonStatus reads the heartbeat (zero value when there is none).
func LoadDaemonStatus(home string) DaemonStatus {
	var s DaemonStatus
	_ = loadJSON(daemonStatusPath(home), &s)
	return s
}

// SaveDaemonStatus writes the heartbeat.
func SaveDaemonStatus(home string, s DaemonStatus) error {
	return saveJSON(daemonStatusPath(home), &s)
}

// Fail records an error (kept until a later cycle succeeds).
func (s *DaemonStatus) Fail(what string, err error) {
	if err == nil {
		return
	}
	s.LastError, s.ErrorAt = what+": "+err.Error(), time.Now().Unix()
}
