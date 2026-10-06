package dbrecord

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// testDSN is read from PARITY_TEST_DSN, defaulting to a local database
// named parity_test (created for this phase's spike — see
// docs/plans/phase-6.md). Tests in this package are skipped, not failed,
// when no Postgres is reachable, consistent with ADR 0002's "fail-skip-
// with-a-clear-message" pattern for infrastructure this dev environment
// may not always have.
func testDSN() string {
	if v := os.Getenv("PARITY_TEST_DSN"); v != "" {
		return v
	}
	return "postgres://localhost:5432/parity_test?sslmode=disable"
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", testDSN())
	if err != nil {
		t.Skipf("skipping: cannot open test Postgres DSN: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("skipping: no reachable Postgres at %s: %v", testDSN(), err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func setupOrdersTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		DROP TABLE IF EXISTS dbrecord_spike_orders;
		CREATE TABLE dbrecord_spike_orders (
			id SERIAL PRIMARY KEY,
			user_id INT NOT NULL,
			total NUMERIC NOT NULL,
			status TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("setup table: %v", err)
	}
}

func TestRealDB_ExecAndQueryAgainstRealPostgres(t *testing.T) {
	db := openTestDB(t)
	setupOrdersTable(t, db)

	real := &RealDB{Conn: db}
	ctx := context.Background()

	res, err := real.ExecContext(ctx, "INSERT INTO dbrecord_spike_orders (user_id, total, status) VALUES ($1, $2, $3)", 7, 19.99, "pending")
	if err != nil {
		t.Fatalf("ExecContext: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected 1 row affected, got %d", res.RowsAffected)
	}

	rows, err := real.QueryContext(ctx, "SELECT id, user_id, total, status FROM dbrecord_spike_orders WHERE user_id = $1", 7)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	if len(rows.Values) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows.Values))
	}
}

func TestRecordingDB_LogsToJSONL(t *testing.T) {
	db := openTestDB(t)
	setupOrdersTable(t, db)

	logPath := filepath.Join(t.TempDir(), "queries.jsonl")
	rec, err := NewRecordingDB(&RealDB{Conn: db}, logPath)
	if err != nil {
		t.Fatalf("NewRecordingDB: %v", err)
	}

	ctx := context.Background()
	if _, err := rec.ExecContext(ctx, "INSERT INTO dbrecord_spike_orders (user_id, total, status) VALUES ($1, $2, $3)", 42, 5.0, "paid"); err != nil {
		t.Fatalf("ExecContext: %v", err)
	}
	if _, err := rec.QueryContext(ctx, "SELECT id, user_id, total, status FROM dbrecord_spike_orders WHERE user_id = $1", 42); err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	rec.Close()

	replay, err := LoadReplayDB(logPath)
	if err != nil {
		t.Fatalf("LoadReplayDB: %v", err)
	}
	if len(replay.records) != 2 {
		t.Fatalf("expected 2 recorded calls, got %d", len(replay.records))
	}
}
