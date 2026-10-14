// Package grounded implements classify.Classifier by searching the PR's
// actual diff text for each changed field, instead of (or in addition
// to) classify.Heuristic's purely structural rules. This is the first
// real implementation of the second classifier the package doc comment
// on classify.Heuristic says has to exist eventually — ported from (not
// imported from — see the long comment below) the standalone prototype
// in jex2l/parity-labs' pillar2-grounded-classifier.
//
// No LLM is called here. Grounding is deterministic: if a changed
// path's field name appears as a whole identifier token (case- and
// separator-insensitive, so a JSON tag "in_stock" matches a Go struct
// field "InStock") in an added or removed diff line, that's a citation.
// If every changed path has one, the cluster is LIKELY_INTENDED at
// higher confidence than the heuristic baseline ever assigns, because
// there's now direct textual evidence, not just a structural guess.
// If a cluster can't be grounded at all — no diff hunks were given, or
// none of its paths appear anywhere in them — Classifier falls back to
// Fallback (normally classify.Heuristic{}) rather than returning a bare
// "don't know": grounding is additive confidence, not a replacement for
// the existing structural signal when there's nothing to ground against.
package grounded

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/jex2l/parity/internal/classify"
	"github.com/jex2l/parity/internal/diff"
	"github.com/jex2l/parity/internal/report"
)

// Classifier grounds its verdict in classify.PRContext.DiffHunks. Why
// this isn't literally the same code as pillar2-grounded-classifier's
// grounded.Classifier: that package lives in a separate Go module
// (github.com/jex2l/parity-labs/pillar2-grounded-classifier) and this
// one needs to implement classify.Classifier, whose interface types
// (report.ClusterView, classify.PRContext, classify.Result) live under
// this repo's internal/ tree — Go's package-visibility rules mean no
// external module, including parity-labs, can import them. This is a
// deliberate reimplementation against the real interface, not a copy
// for its own sake; see parity-labs' pillar2 README for the three
// options once considered for closing this gap — vendoring the logic
// here (this file) is the one that got built.
type Classifier struct {
	// Fallback is used when a cluster can't be grounded at all (no
	// DiffHunks, or no changed path appears in them). Nil means "return
	// Uncertain" instead of falling back.
	Fallback classify.Classifier
}

// Classify implements classify.Classifier.
func (g Classifier) Classify(c report.ClusterView, pr classify.PRContext) classify.Result {
	// Matches classify.Heuristic's rule 1 exactly: a 5xx/critical change
	// is unintended regardless of what the diff says — nobody
	// intentionally ships a crash, and grounding doesn't change that
	// judgment, only (via the fallback path below, for anything milder)
	// the confidence and rationale around it.
	if c.Severity == "critical" {
		for _, t := range c.ChangeTypes {
			if t == string(diff.ErrorAppeared) {
				return classify.Result{
					Label:      classify.LikelyUnintended,
					Confidence: 0.95,
					Rationale:  "a previously-successful status now returns a server error (5xx) — this is essentially never an intended change",
				}
			}
		}
	}

	if len(pr.DiffHunks) == 0 {
		return g.fallback(c, pr, "no diff hunks were available to ground this classification")
	}

	var found, missing []string
	citations := make([]string, 0, len(c.ChangedPaths))
	for _, path := range c.ChangedPaths {
		leaf := leafField(path)
		if cite, ok := findCitation(leaf, pr.DiffHunks); ok {
			found = append(found, path)
			citations = append(citations, cite)
		} else {
			missing = append(missing, path)
		}
	}

	switch {
	case len(found) == 0:
		return g.fallback(c, pr, fmt.Sprintf("no PR diff line mentions any of %s", strings.Join(c.ChangedPaths, ", ")))
	case len(missing) > 0:
		return classify.Result{
			Label:      classify.Uncertain,
			Confidence: 0.3,
			Rationale:  fmt.Sprintf("grounded evidence found for %s (%s) but none for %s — partial grounding isn't enough to call this intended or not", strings.Join(found, ", "), strings.Join(citations, "; "), strings.Join(missing, ", ")),
		}
	default:
		return classify.Result{
			Label:      classify.LikelyIntended,
			Confidence: 0.8,
			Rationale:  fmt.Sprintf("every changed path is directly evidenced in the PR diff: %s", strings.Join(citations, "; ")),
		}
	}
}

func (g Classifier) fallback(c report.ClusterView, pr classify.PRContext, reason string) classify.Result {
	if g.Fallback == nil {
		return classify.Result{Label: classify.Uncertain, Confidence: 0.0, Rationale: reason + "; no fallback classifier configured"}
	}
	res := g.Fallback.Classify(c, pr)
	res.Rationale = fmt.Sprintf("(%s — falling back to structural heuristic) %s", reason, res.Rationale)
	return res
}

func findCitation(leaf string, hunks []classify.DiffHunk) (string, bool) {
	for _, h := range hunks {
		for _, line := range h.Added {
			if mentionsField(line, leaf) {
				return fmt.Sprintf("%s: %q", h.File, strings.TrimSpace(line)), true
			}
		}
		for _, line := range h.Removed {
			if mentionsField(line, leaf) {
				return fmt.Sprintf("%s: %q", h.File, strings.TrimSpace(line)), true
			}
		}
	}
	return "", false
}

// mentionsField reports whether leaf appears as a whole identifier
// token in line, comparing case- and underscore-insensitively so a
// JSON tag like "in_stock" matches a Go struct field "InStock", or
// "sku" matches "SKU". Matching whole tokens (not substrings) is what
// keeps "id" from matching inside "valid" — a risk the original
// word-boundary-byte-scanning approach in pillar2-grounded-classifier
// also had to guard against; this tokenizer avoids it by construction.
func mentionsField(line, leaf string) bool {
	if leaf == "" {
		return false
	}
	normLeaf := normalizeIdent(leaf)
	for _, tok := range identTokens(line) {
		if normalizeIdent(tok) == normLeaf {
			return true
		}
	}
	return false
}

func normalizeIdent(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' || r == '-' {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// identTokens splits line into identifier-like tokens (letters, digits,
// underscores — so "price_cents" stays one token, not two).
func identTokens(line string) []string {
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range line {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return toks
}

// leafField extracts the final field name from a JSON path like
// "$.user.address.zip" or "$[id=1].sku", matching the path convention
// internal/diff already produces.
func leafField(path string) string {
	path = strings.TrimPrefix(path, "$.")
	path = strings.TrimPrefix(path, "$")
	if i := strings.LastIndexByte(path, '.'); i != -1 {
		path = path[i+1:]
	}
	if i := strings.IndexByte(path, '['); i != -1 {
		path = path[:i]
	}
	return path
}
