package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeReportJSON_ReadsFreshFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	os.WriteFile(path, []byte(`{"total_replayed":1}`), 0o644)

	rec := httptest.NewRecorder()
	serveReportJSON(rec, path)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != `{"total_replayed":1}` {
		t.Fatalf("body = %q", rec.Body.String())
	}

	// Rewriting the file (as a new `parity replay` run would) must be
	// reflected on the next request without restarting the server —
	// this is the whole point of re-reading from disk rather than
	// caching the report at startup.
	os.WriteFile(path, []byte(`{"total_replayed":2}`), 0o644)
	rec2 := httptest.NewRecorder()
	serveReportJSON(rec2, path)
	if rec2.Body.String() != `{"total_replayed":2}` {
		t.Fatalf("expected the updated file content on a fresh request, got %q", rec2.Body.String())
	}
}

func TestServeReportJSON_MissingFileIs404(t *testing.T) {
	rec := httptest.NewRecorder()
	serveReportJSON(rec, filepath.Join(t.TempDir(), "does-not-exist.json"))
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestServeReportJSON_InvalidJSONIs500(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte(`not json`), 0o644)

	rec := httptest.NewRecorder()
	serveReportJSON(rec, path)
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500 for invalid JSON", rec.Code)
	}
}

func TestServeRunList_ListsSiblingJSONFilesAndMarksCurrent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "report.json"), []byte(`{}`), 0o644)
	os.WriteFile(filepath.Join(dir, "report-2.json"), []byte(`{}`), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(`ignore me`), 0o644)

	rec := httptest.NewRecorder()
	serveRunList(rec, dir, filepath.Join(dir, "report.json"))

	body := rec.Body.String()
	if !strings.Contains(body, `"file":"report-2.json"`) || !strings.Contains(body, `"file":"report.json"`) {
		t.Fatalf("expected both json files listed, got %s", body)
	}
	if strings.Contains(body, "notes.txt") {
		t.Fatalf("non-JSON files must not be listed, got %s", body)
	}
	if !strings.Contains(body, `"current":true`) {
		t.Fatalf("expected the current report to be marked, got %s", body)
	}
}
