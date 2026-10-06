package dbrecord

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// TestReplayDB_ServesRecordedResultWithoutRealDB is the Phase 6 spike's
// core demonstration: record a write (INSERT) and the read that depends
// on it against a real Postgres, then show that ReplayDB reproduces the
// read's result with zero live database involvement.
func TestReplayDB_ServesRecordedResultWithoutRealDB(t *testing.T) {
	db := openTestDB(t)
	setupOrdersTable(t, db)

	logPath := filepath.Join(t.TempDir(), "queries.jsonl")
	rec, err := NewRecordingDB(&RealDB{Conn: db}, logPath)
	if err != nil {
		t.Fatalf("NewRecordingDB: %v", err)
	}
	ctx := context.Background()
	if _, err := rec.ExecContext(ctx, "INSERT INTO dbrecord_spike_orders (user_id, total, status) VALUES ($1, $2, $3)", 99, 42.5, "pending"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	liveRows, err := rec.QueryContext(ctx, "SELECT user_id, total, status FROM dbrecord_spike_orders WHERE user_id = $1", 99)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rec.Close()

	replay, err := LoadReplayDB(logPath)
	if err != nil {
		t.Fatalf("LoadReplayDB: %v", err)
	}
	replayedRows, err := replay.QueryContext(ctx, "SELECT user_id, total, status FROM dbrecord_spike_orders WHERE user_id = $1", 99)
	if err != nil {
		t.Fatalf("replay query (should have matched the recorded call): %v", err)
	}

	if len(replayedRows.Values) != len(liveRows.Values) {
		t.Fatalf("replayed %d rows, live had %d", len(replayedRows.Values), len(liveRows.Values))
	}
	if !rowsMatch(liveRows, replayedRows) {
		t.Fatalf("replayed rows do not match live rows: replayed=%v live=%v", replayedRows.Values, liveRows.Values)
	}
}

// TestFidelity_SnapshotOnlyVsRecordedReplay is the headline Phase 6
// measurement: on a snapshot taken BEFORE the write happened, a
// snapshot-only replay of the dependent read finds nothing (the honest
// failure mode of stateful replay without DB recording), while a replay
// fed from the DB recording reproduces the original response exactly.
// Numbers are logged, not just asserted, per docs/plans/phase-6.md's
// "measured and reported with real numbers" acceptance bar.
func TestFidelity_SnapshotOnlyVsRecordedReplay(t *testing.T) {
	db := openTestDB(t)
	setupOrdersTable(t, db) // this IS the "snapshot taken before the write"

	// Simulate production: a write happens, then a read depends on it.
	// We record both through RecordingDB.
	logPath := filepath.Join(t.TempDir(), "queries.jsonl")
	rec, err := NewRecordingDB(&RealDB{Conn: db}, logPath)
	if err != nil {
		t.Fatalf("NewRecordingDB: %v", err)
	}
	ctx := context.Background()
	if _, err := rec.ExecContext(ctx, "INSERT INTO dbrecord_spike_orders (user_id, total, status) VALUES ($1, $2, $3)", 7, 100.0, "paid"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	originalRows, err := rec.QueryContext(ctx, "SELECT user_id, total, status FROM dbrecord_spike_orders WHERE user_id = $1", 7)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rec.Close()
	if len(originalRows.Values) != 1 {
		t.Fatalf("setup invariant broken: expected the recorded read to see the row it depends on")
	}

	// Scenario A: snapshot-only. Replay the SAME read against a database
	// restored from a snapshot taken before the write (we simulate this
	// by truncating the table — "before the INSERT happened").
	if _, err := db.Exec("TRUNCATE dbrecord_spike_orders"); err != nil {
		t.Fatalf("truncate (simulating pre-write snapshot restore): %v", err)
	}
	snapshotOnly := &RealDB{Conn: db}
	snapshotRows, err := snapshotOnly.QueryContext(ctx, "SELECT user_id, total, status FROM dbrecord_spike_orders WHERE user_id = $1", 7)
	if err != nil {
		t.Fatalf("snapshot-only query: %v", err)
	}

	// Scenario B: replay with DB query/result recording.
	replay, err := LoadReplayDB(logPath)
	if err != nil {
		t.Fatalf("LoadReplayDB: %v", err)
	}
	recordedReplayRows, err := replay.QueryContext(ctx, "SELECT user_id, total, status FROM dbrecord_spike_orders WHERE user_id = $1", 7)
	if err != nil {
		t.Fatalf("recorded-replay query: %v", err)
	}

	snapshotFidelity := rowsMatch(originalRows, snapshotRows)
	recordedFidelity := rowsMatch(originalRows, recordedReplayRows)

	t.Logf("snapshot-only fidelity for this write-dependent read: %v (got %d rows, original had %d)", snapshotFidelity, len(snapshotRows.Values), len(originalRows.Values))
	t.Logf("DB-recording replay fidelity for this write-dependent read: %v (got %d rows, original had %d)", recordedFidelity, len(recordedReplayRows.Values), len(originalRows.Values))

	if snapshotFidelity {
		t.Fatalf("expected snapshot-only replay to FAIL to reproduce a write-dependent read (that's the whole point of this spike) — got a match instead, test setup is wrong")
	}
	if !recordedFidelity {
		t.Fatalf("expected DB-recording replay to exactly reproduce the write-dependent read, but it didn't")
	}
}

// rowsMatch compares by formatted value rather than strict interface{}
// equality: a Rows that went through the JSONL recording round-trips
// numbers as float64, while a live *Rows from RealDB may carry int32/
// int64 from the driver — same value, different concrete Go type. The
// product's own diff engine (internal/diff) makes exactly this same
// choice for JSON comparison; this spike mirrors it rather than
// reinventing a separate rule.
func rowsMatch(a, b *Rows) bool {
	if len(a.Values) != len(b.Values) {
		return false
	}
	for i := range a.Values {
		if len(a.Values[i]) != len(b.Values[i]) {
			return false
		}
		for j := range a.Values[i] {
			if fmt.Sprintf("%v", a.Values[i][j]) != fmt.Sprintf("%v", b.Values[i][j]) {
				return false
			}
		}
	}
	return true
}

// TestReplayDB_UnmatchedCallReturnsHonestError proves ReplayDB never
// fabricates a plausible-looking empty result for an unrecorded query —
// it's an explicit, named error (ErrNoMatch), not a silent empty Rows{}
// that would look identical to "the query legitimately returned nothing."
func TestReplayDB_UnmatchedCallReturnsHonestError(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "empty.jsonl")
	db := openTestDB(t)
	setupOrdersTable(t, db)
	rec, _ := NewRecordingDB(&RealDB{Conn: db}, logPath)
	rec.Close() // no calls recorded

	replay, err := LoadReplayDB(logPath)
	if err != nil {
		t.Fatalf("LoadReplayDB: %v", err)
	}
	_, err = replay.QueryContext(context.Background(), "SELECT 1", )
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch for an unrecorded query, got %v", err)
	}
}
