// Package classify labels diff clusters as likely intended or unintended,
// so reviewers (or a coding agent) see unintended-looking diffs first.
// Per spec 6.7.1, a heuristic baseline must exist before any LLM-based
// classifier; this package is that baseline. The LLM-based classifier
// (spec 6.7.2) is a separate implementation of the same Classifier
// interface, not built in this repo (no model endpoint configured here —
// see docs/plans/phase-4.md).
package classify

import (
	"strings"

	"github.com/jex2l/parity/internal/diff"
	"github.com/jex2l/parity/internal/report"
)

// Label is the classifier's verdict on a cluster.
type Label string

const (
	LikelyIntended   Label = "LIKELY_INTENDED"
	LikelyUnintended Label = "LIKELY_UNINTENDED"
	Uncertain        Label = "UNCERTAIN"
)

// Result is one classification.
type Result struct {
	Label      Label
	Confidence float64 // in [0,1]; see docs/plans/phase-4.md on calibration status
	Rationale  string
}

// PRContext is what the classifier is given about the PR producing the
// candidate build, per spec 6.7: changed file paths, title, description.
// No raw diff hunks are required for the heuristic baseline.
type PRContext struct {
	ChangedFiles []string
	Title        string
	Description  string
}

// Classifier labels one cluster.
type Classifier interface {
	Classify(c report.ClusterView, pr PRContext) Result
}

// Heuristic is the required-to-exist-first baseline classifier (spec
// 6.7.1): no ML, just structural rules over the cluster and PR metadata.
type Heuristic struct{}

// Classify implements Classifier.
func (Heuristic) Classify(c report.ClusterView, pr PRContext) Result {
	// Rule 1: a 5xx/critical change is unintended regardless of what the
	// PR touched — nobody intentionally ships a crash.
	if c.Severity == "critical" {
		for _, t := range c.ChangeTypes {
			if t == string(diff.ErrorAppeared) {
				return Result{Label: LikelyUnintended, Confidence: 0.95, Rationale: "a previously-successful status now returns a server error (5xx) — this is essentially never an intended change"}
			}
		}
	}

	touchedRelatedFile := anyFileTouchesEndpoint(pr.ChangedFiles, c.Endpoint)

	// Rule 2: field/structure removed or type-changed in code the PR
	// didn't touch is a strong unintended signal.
	hasRemovalOrTypeChange := hasAny(c.ChangeTypes, string(diff.FieldRemoved), string(diff.TypeChanged))
	if hasRemovalOrTypeChange && !touchedRelatedFile {
		return Result{Label: LikelyUnintended, Confidence: 0.8, Rationale: "a field was removed or changed type, but no file touched by this PR appears related to " + c.Endpoint}
	}

	// Rule 3: only additive, low-severity changes (new field) in code the
	// PR did touch look intended.
	onlyAdditions := allEqual(c.ChangeTypes, string(diff.FieldAdded))
	if onlyAdditions && touchedRelatedFile {
		return Result{Label: LikelyIntended, Confidence: 0.75, Rationale: "only new fields were added, in code this PR's changed files plausibly touch"}
	}
	if onlyAdditions && c.Severity == "low" {
		return Result{Label: LikelyIntended, Confidence: 0.55, Rationale: "only new fields were added (low severity), consistent with an intentional additive change even though it couldn't be tied to a specific changed file"}
	}

	// Rule 4: a removal/type-change the PR's files do plausibly explain
	// is still worth a human look, but leans intended.
	if hasRemovalOrTypeChange && touchedRelatedFile {
		return Result{Label: LikelyIntended, Confidence: 0.5, Rationale: "a field was removed or changed type in code this PR's changed files plausibly touch — likely intentional, but field removals should still be reviewed"}
	}

	return Result{Label: Uncertain, Confidence: 0.3, Rationale: "no strong structural signal either way; needs human (or LLM classifier) judgment"}
}

func anyFileTouchesEndpoint(files []string, endpoint string) bool {
	tokens := endpointTokens(endpoint)
	for _, f := range files {
		lf := strings.ToLower(f)
		for _, tok := range tokens {
			if tok != "" && strings.Contains(lf, tok) {
				return true
			}
		}
	}
	return false
}

// endpointTokens extracts plausible identifying tokens from an endpoint
// template, e.g. "/orders/{id}" -> ["orders"].
func endpointTokens(endpoint string) []string {
	parts := strings.Split(endpoint, "/")
	var out []string
	for _, p := range parts {
		p = strings.Trim(p, "{}")
		if p == "" || p == "id" {
			continue
		}
		out = append(out, strings.ToLower(p))
	}
	return out
}

func hasAny(haystack []string, needles ...string) bool {
	set := map[string]bool{}
	for _, n := range needles {
		set[n] = true
	}
	for _, h := range haystack {
		if set[h] {
			return true
		}
	}
	return false
}

func allEqual(haystack []string, want string) bool {
	if len(haystack) == 0 {
		return false
	}
	for _, h := range haystack {
		if h != want {
			return false
		}
	}
	return true
}
