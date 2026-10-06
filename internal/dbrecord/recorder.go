package dbrecord

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Record is one recorded database call, as written to the JSONL log.
type Record struct {
	Op     string   `json:"op"` // "exec" or "query"
	SQL    string   `json:"sql"`
	Args   []any    `json:"args"`
	Exec   *ExecResult `json:"exec,omitempty"`
	Query  *Rows    `json:"query,omitempty"`
	ErrMsg string   `json:"err,omitempty"`
}

// RecordingDB wraps a real DB and logs every call to a JSONL file before
// returning the real result to the caller.
type RecordingDB struct {
	Inner DB
	f     *os.File
	enc   *json.Encoder
}

// NewRecordingDB creates a RecordingDB that logs to path (truncated).
func NewRecordingDB(inner DB, path string) (*RecordingDB, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("dbrecord: create log %s: %w", path, err)
	}
	return &RecordingDB{Inner: inner, f: f, enc: json.NewEncoder(f)}, nil
}

func (r *RecordingDB) Close() error { return r.f.Close() }

func (r *RecordingDB) ExecContext(ctx context.Context, query string, args ...any) (ExecResult, error) {
	res, err := r.Inner.ExecContext(ctx, query, args...)
	rec := Record{Op: "exec", SQL: normalizeSQL(query), Args: args}
	if err != nil {
		rec.ErrMsg = err.Error()
	} else {
		rec.Exec = &res
	}
	r.enc.Encode(rec) // best-effort: a logging failure must not break the caller
	return res, err
}

func (r *RecordingDB) QueryContext(ctx context.Context, query string, args ...any) (*Rows, error) {
	res, err := r.Inner.QueryContext(ctx, query, args...)
	rec := Record{Op: "query", SQL: normalizeSQL(query), Args: args}
	if err != nil {
		rec.ErrMsg = err.Error()
	} else {
		rec.Query = res
	}
	r.enc.Encode(rec)
	return res, err
}

// normalizeSQL collapses whitespace so semantically-identical queries
// written with different formatting still match on replay.
func normalizeSQL(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}
