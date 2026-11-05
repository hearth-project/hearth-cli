// Package datatool is a client for a DataTool-compatible transfer service:
// it copies a directory from one storage system to another on the service's
// side (asynchronous tasks, progress by polling), without the data passing
// through this machine or the Hearth server.
package datatool

import "time"

// Task states reported by the service.
const (
	StatusPending   = "pending"
	StatusServing   = "serving"
	StatusRunning   = "running"
	StatusCleaning  = "cleaning"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCanceling = "canceling"
	StatusCanceled  = "canceled"
)

// Finished reports a state a task never leaves.
func Finished(status string) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCanceled:
		return true
	}
	return false
}

// StoragePath is a path on a named storage system ("gpfs@cn" ...).
type StoragePath struct {
	Storage string `json:"storage"`
	Path    string `json:"path"`
}

// Options of one task. The service has defaults and limits (Meta).
type Options struct {
	Transfers int  `json:"transfers,omitempty"`
	Checkers  int  `json:"checkers,omitempty"`
	DryRun    bool `json:"dryRun,omitempty"`
}

// Pair is one allowed direction between two storages.
type Pair struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

// Limits the service enforces.
type Limits struct {
	NameMaxLen   int `json:"NameMaxLen"`
	PathMaxLen   int `json:"PathMaxLen"`
	TransfersMax int `json:"TransfersMax"`
	CheckersMax  int `json:"CheckersMax"`
}

// Meta describes what the service can do.
type Meta struct {
	Storages       []string          `json:"storages"`
	ValidPairs     []Pair            `json:"validPairs"`
	PathHints      map[string]string `json:"pathHints"`
	DefaultOptions Options           `json:"defaultOptions"`
	Limits         Limits            `json:"limits"`
	// DestinationEmptyHint is the service's reminder that the target
	// directory must be empty.
	DestinationEmptyHint string `json:"destinationEmptyHint"`
}

// Progress of a running task.
type Progress struct {
	Bytes        int64     `json:"bytes"`
	TotalBytes   int64     `json:"totalBytes"`
	Transfers    int       `json:"transfers"`
	TotalObjects int       `json:"totalObjects"`
	SpeedBps     float64   `json:"speedBps"`
	ETASeconds   int64     `json:"etaSeconds"`
	Errors       int       `json:"errors"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Task is one copy job.
type Task struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Creator   string      `json:"creator"`
	Src       StoragePath `json:"src"`
	Dst       StoragePath `json:"dst"`
	Options   Options     `json:"options"`
	Status    string      `json:"status"`
	Progress  Progress    `json:"progress"`
	Message   string      `json:"message"`
	ErrorCode string      `json:"errorCode"`
	// ServeAddr is the storage-side endpoint the service picked.
	ServeAddr  string     `json:"serveAddr"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

// CreateRequest is the body of a new task.
type CreateRequest struct {
	Name    string      `json:"name"`
	Src     StoragePath `json:"src"`
	Dst     StoragePath `json:"dst"`
	Secrets *Secrets    `json:"secrets,omitempty"`
	Options Options     `json:"options"`
}

// Secrets travel only in the create request; they are never stored or
// printed by Hearth.
type Secrets struct {
	AFSUGI string `json:"afsUgi,omitempty"`
}

// TaskQuery filters a task list.
type TaskQuery struct {
	Keyword  string
	Status   string
	Page     int
	PageSize int
}
