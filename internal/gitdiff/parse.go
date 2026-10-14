// Package gitdiff parses the output of `git diff` into
// classify.DiffHunk values, so classify/grounded has something to
// search for evidence in. Deliberately minimal: it doesn't model hunk
// headers, line numbers, or rename/binary-file markers — only "which
// file, which lines were added, which were removed" survive, which is
// all classify/grounded's token matching needs.
package gitdiff

import (
	"strings"

	"github.com/jex2l/parity/internal/classify"
)

// Parse reads raw unified diff text (e.g. the output of
// `git diff --unified=0 origin/main...HEAD`) and returns one DiffHunk
// per file, aggregating all of that file's added/removed lines across
// however many hunks it had.
func Parse(diffText string) []classify.DiffHunk {
	var hunks []classify.DiffHunk
	byFile := map[string]*classify.DiffHunk{}
	order := []string{}

	currentFile := ""
	get := func(file string) *classify.DiffHunk {
		if h, ok := byFile[file]; ok {
			return h
		}
		h := &classify.DiffHunk{File: file}
		byFile[file] = h
		order = append(order, file)
		return h
	}

	for _, line := range strings.Split(diffText, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			currentFile = stripDiffPathPrefix(strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "--- "):
			// Only used to detect a file deletion (+++ /dev/null); the
			// real "current file" tracking happens on the +++ line,
			// which always follows --- for a given file in unified diff
			// output, so --- itself doesn't need to set currentFile.
		case strings.HasPrefix(line, "diff --git "):
			currentFile = ""
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			// handled above; unreachable given the HasPrefix checks
			// ordering, kept only so a future reordering doesn't
			// silently fall through to the +/- cases below and
			// misinterpret "+++"/"---" markers as content lines.
		case currentFile != "" && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			h := get(currentFile)
			h.Added = append(h.Added, strings.TrimPrefix(line, "+"))
		case currentFile != "" && strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			h := get(currentFile)
			h.Removed = append(h.Removed, strings.TrimPrefix(line, "-"))
		}
	}

	for _, f := range order {
		hunks = append(hunks, *byFile[f])
	}
	return hunks
}

// stripDiffPathPrefix removes git's "a/"/"b/" prefix and handles
// "/dev/null" (a pure addition or deletion has one side as /dev/null).
func stripDiffPathPrefix(path string) string {
	path = strings.TrimSpace(path)
	if path == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}
