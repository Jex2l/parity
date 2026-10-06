// Package feedback stores reviewer feedback on clusters ("this was
// intended" / "this was a bug"), per spec 6.7.4. v1 is a file-backed
// store keyed by project; a real control plane (Phase 4+ beyond this
// repo's scope) would back this with Postgres instead, per
// docs/plans/phase-4.md.
package feedback

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Verdict is what a reviewer said about a cluster.
type Verdict string

const (
	VerdictAcknowledgedIntended Verdict = "acknowledged_intended"
	VerdictBug                  Verdict = "bug"
)

// Entry is one piece of feedback.
type Entry struct {
	ClusterKey string    `json:"cluster_key"` // diff.Cluster.Key — stable across runs with the same endpoint+paths+types
	Verdict    Verdict   `json:"verdict"`
	Reason     string    `json:"reason,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Store persists feedback to a single JSON file. Safe for concurrent use
// within one process (file locking across processes is not implemented —
// a real control plane would use a database transaction instead).
type Store struct {
	path string
	mu   sync.Mutex
}

// Open loads (or creates) a Store backed by the file at path.
func Open(path string) (*Store, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
			return nil, fmt.Errorf("feedback: create %s: %w", path, err)
		}
	}
	return &Store{path: path}, nil
}

func (s *Store) load() ([]Entry, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) save(entries []Entry) error {
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

// Record appends (or replaces, if clusterKey already has an entry) a
// feedback entry.
func (s *Store) Record(clusterKey string, verdict Verdict, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.load()
	if err != nil {
		return fmt.Errorf("feedback: load: %w", err)
	}
	replaced := false
	for i, e := range entries {
		if e.ClusterKey == clusterKey {
			entries[i] = Entry{ClusterKey: clusterKey, Verdict: verdict, Reason: reason, RecordedAt: time.Now().UTC()}
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, Entry{ClusterKey: clusterKey, Verdict: verdict, Reason: reason, RecordedAt: time.Now().UTC()})
	}
	return s.save(entries)
}

// IsAcknowledgedIntended reports whether clusterKey was previously marked
// intended, used to suppress identical clusters in future runs of the
// same project (spec 6.7.4(a)).
func (s *Store) IsAcknowledgedIntended(clusterKey string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.load()
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.ClusterKey == clusterKey {
			return e.Verdict == VerdictAcknowledgedIntended, nil
		}
	}
	return false, nil
}

// All returns every recorded entry, for use as classifier training/eval
// data (spec 6.7.4(b)).
func (s *Store) All() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}
