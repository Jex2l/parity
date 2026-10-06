package retention

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func touch(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestPurge_DeletesOnlyOldSegments(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	oldSeg := filepath.Join(dir, "seg-old.zst")
	oldIdx := oldSeg + ".idx"
	newSeg := filepath.Join(dir, "seg-new.zst")

	touch(t, oldSeg, now.Add(-48*time.Hour))
	touch(t, oldIdx, now.Add(-48*time.Hour))
	touch(t, newSeg, now.Add(-1*time.Hour))
	touch(t, filepath.Join(dir, "not-a-segment.txt"), now.Add(-100*time.Hour)) // must be ignored

	res, err := Purge(dir, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}

	if len(res.Deleted) != 1 || res.Deleted[0] != oldSeg {
		t.Fatalf("expected only %s deleted, got %+v", oldSeg, res.Deleted)
	}
	if len(res.Kept) != 1 || res.Kept[0] != newSeg {
		t.Fatalf("expected only %s kept, got %+v", newSeg, res.Kept)
	}
	if _, err := os.Stat(oldSeg); !os.IsNotExist(err) {
		t.Fatalf("expected %s to actually be removed from disk", oldSeg)
	}
	if _, err := os.Stat(oldIdx); !os.IsNotExist(err) {
		t.Fatalf("expected sidecar index %s to be removed alongside its segment", oldIdx)
	}
	if _, err := os.Stat(newSeg); err != nil {
		t.Fatalf("expected %s to still exist, got %v", newSeg, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "not-a-segment.txt")); err != nil {
		t.Fatalf("non-segment files must never be touched by purge, got %v", err)
	}
}

func TestPurge_EmptyDirIsNoOp(t *testing.T) {
	dir := t.TempDir()
	res, err := Purge(dir, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("Purge on empty dir: %v", err)
	}
	if len(res.Deleted) != 0 || len(res.Kept) != 0 {
		t.Fatalf("expected no-op on an empty directory, got %+v", res)
	}
}
