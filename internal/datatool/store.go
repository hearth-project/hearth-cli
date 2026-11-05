package datatool

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxRecords bounds the local history.
const maxRecords = 500

// Record is a task this machine submitted. It holds no credentials: only
// what is needed to find the task again and to tell it from other people's.
type Record struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Src         StoragePath `json:"src"`
	Dst         StoragePath `json:"dst"`
	DryRun      bool        `json:"dry_run,omitempty"`
	SubmittedAt time.Time   `json:"submitted_at"`
	Host        string      `json:"host,omitempty"`
}

// Store keeps the records of submitted tasks in one JSON file (mode 0600).
// The service does not tell users apart, so this is how `hearth transfer`
// knows which tasks are yours.
type Store struct {
	Path string
	mu   sync.Mutex
}

// List returns the records, newest first.
func (s *Store) List() ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() ([]Record, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recs []Record
	if err := json.Unmarshal(b, &recs); err != nil {
		return nil, err
	}
	return recs, nil
}

// Has reports whether this machine submitted the task.
func (s *Store) Has(id string) bool {
	recs, _ := s.List()
	for _, r := range recs {
		if r.ID == id {
			return true
		}
	}
	return false
}

// Add puts a record first, dropping the oldest beyond the bound.
func (s *Store) Add(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.load()
	if err != nil {
		return err
	}
	out := []Record{r}
	for _, o := range recs {
		if o.ID != r.ID {
			out = append(out, o)
		}
	}
	if len(out) > maxRecords {
		out = out[:maxRecords]
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".transfers-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}
