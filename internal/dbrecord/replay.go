package dbrecord

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// ErrNoMatch is returned when a replay query/exec has no matching
// recorded call — the honest signal that this request's DB dependency
// wasn't captured, rather than silently returning an empty result.
var ErrNoMatch = fmt.Errorf("dbrecord: no matching recorded call")

// ReplayDB serves recorded Records instead of talking to a real
// database. Matching is (normalized SQL, args) equality; see
// docs/limits.md for what happens on a near-miss (nothing clever —
// ErrNoMatch, by design, so callers can tell).
type ReplayDB struct {
	records []Record
}

// LoadReplayDB reads a JSONL recording produced by RecordingDB.
func LoadReplayDB(path string) (*ReplayDB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("dbrecord: open %s: %w", path, err)
	}
	defer f.Close()

	var records []Record
	dec := json.NewDecoder(f)
	for dec.More() {
		var r Record
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("dbrecord: decode record: %w", err)
		}
		records = append(records, r)
	}
	return &ReplayDB{records: records}, nil
}

func (r *ReplayDB) find(op, query string, args []any) (*Record, bool) {
	normalized := normalizeSQL(query)
	for i := range r.records {
		rec := &r.records[i]
		if rec.Op == op && rec.SQL == normalized && argsEqual(rec.Args, args) {
			return rec, true
		}
	}
	return nil, false
}

func argsEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if fmt.Sprintf("%v", a[i]) != fmt.Sprintf("%v", b[i]) {
			return false
		}
	}
	return true
}

func (r *ReplayDB) ExecContext(ctx context.Context, query string, args ...any) (ExecResult, error) {
	rec, ok := r.find("exec", query, args)
	if !ok {
		return ExecResult{}, ErrNoMatch
	}
	if rec.ErrMsg != "" {
		return ExecResult{}, fmt.Errorf("dbrecord: recorded error: %s", rec.ErrMsg)
	}
	return *rec.Exec, nil
}

func (r *ReplayDB) QueryContext(ctx context.Context, query string, args ...any) (*Rows, error) {
	rec, ok := r.find("query", query, args)
	if !ok {
		return &Rows{}, ErrNoMatch
	}
	if rec.ErrMsg != "" {
		return nil, fmt.Errorf("dbrecord: recorded error: %s", rec.ErrMsg)
	}
	return rec.Query, nil
}
