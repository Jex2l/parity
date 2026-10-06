// Package retention implements segment retention/purge by age, per spec
// 6.?/8 ("retention and deletion (configurable TTL on segments, `parity
// purge`)").
package retention

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Result summarizes one Purge call.
type Result struct {
	Deleted []string
	Kept    []string
	Errors  []string
}

// Purge deletes every segment file (and its sidecar `.idx` file) under
// dir whose modification time is older than olderThan, relative to now.
// Returns what was deleted/kept so callers (CLI, tests) can report or
// assert on it; never deletes anything outside dir.
func Purge(dir string, olderThan time.Duration, now time.Time) (Result, error) {
	var res Result
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, fmt.Errorf("retention: read %s: %w", dir, err)
	}

	cutoff := now.Add(-olderThan)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zst") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if info.ModTime().After(cutoff) {
			res.Kept = append(res.Kept, path)
			continue
		}
		if err := os.Remove(path); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		idxPath := path + ".idx"
		if _, err := os.Stat(idxPath); err == nil {
			os.Remove(idxPath) // best-effort; the segment itself is already gone either way
		}
		res.Deleted = append(res.Deleted, path)
	}
	return res, nil
}
