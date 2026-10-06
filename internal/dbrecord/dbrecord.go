// Package dbrecord is the Phase 6 spike: it records database query/result
// pairs at the database/sql call boundary (not the Postgres wire
// protocol itself — see docs/plans/phase-6.md for why) and can replay
// them without a live database. This lets a write-dependent read (e.g.
// "fetch the order I just created") replay correctly even against a
// database snapshot taken before that write happened.
package dbrecord

import (
	"context"
	"database/sql"
	"fmt"
)

// ExecResult is the recordable subset of sql.Result.
type ExecResult struct {
	LastInsertID int64
	RowsAffected int64
}

// Rows is a fully-materialized query result (eager, not streaming — fine
// at the row counts this spike deals with; a production version would
// stream). Columns and Values are JSON-friendly ([]any per row).
type Rows struct {
	Columns []string
	Values  [][]any
}

// DB is the narrow interface this spike records/replays against. Only
// code written against DB (not raw *sql.DB) benefits from
// record/replay — a real implementation would need to cover more of
// database/sql's surface (transactions, prepared statements) to be a
// drop-in replacement. Scoped intentionally for the spike.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (ExecResult, error)
	QueryContext(ctx context.Context, query string, args ...any) (*Rows, error)
}

// RealDB adapts a real *sql.DB to the DB interface, materializing query
// results eagerly.
type RealDB struct {
	Conn *sql.DB
}

func (r *RealDB) ExecContext(ctx context.Context, query string, args ...any) (ExecResult, error) {
	res, err := r.Conn.ExecContext(ctx, query, args...)
	if err != nil {
		return ExecResult{}, err
	}
	id, _ := res.LastInsertId() // Postgres driver may not support this; 0 if not
	affected, _ := res.RowsAffected()
	return ExecResult{LastInsertID: id, RowsAffected: affected}, nil
}

func (r *RealDB) QueryContext(ctx context.Context, query string, args ...any) (*Rows, error) {
	rows, err := r.Conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("dbrecord: columns: %w", err)
	}
	result := &Rows{Columns: cols}
	for rows.Next() {
		scanTargets := make([]any, len(cols))
		scanPtrs := make([]any, len(cols))
		for i := range scanTargets {
			scanPtrs[i] = &scanTargets[i]
		}
		if err := rows.Scan(scanPtrs...); err != nil {
			return nil, fmt.Errorf("dbrecord: scan: %w", err)
		}
		result.Values = append(result.Values, normalizeRow(scanTargets))
	}
	return result, rows.Err()
}

// normalizeRow converts driver-returned values (which may be []byte for
// text-ish columns) into JSON-friendly types so recorded JSONL is
// readable and round-trips through json.Marshal/Unmarshal cleanly.
func normalizeRow(vals []any) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		if b, ok := v.([]byte); ok {
			out[i] = string(b)
		} else {
			out[i] = v
		}
	}
	return out
}
