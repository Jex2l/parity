package grounded

import (
	"strings"
	"testing"

	"github.com/jex2l/parity/internal/classify"
	"github.com/jex2l/parity/internal/report"
)

func TestClassify_StructTagField_LikelyIntended(t *testing.T) {
	// The real shape from jex2l/parity-client-test#4: a Go struct field
	// `InStock bool `json:"in_stock"`` and the map-literal line setting
	// it. The JSON path's leaf ("in_stock") does NOT literally appear in
	// the Go source line ("InStock: true") — only token-normalized
	// matching (case + underscore insensitive) finds this.
	hunks := []classify.DiffHunk{
		{
			File:  "cmd/widget-api/main.go",
			Added: []string{`			{ID: 1, Name: "bolt", Price: 199, InStock: true},`},
		},
	}
	c := report.ClusterView{Endpoint: "/widgets", ChangedPaths: []string{"$[id=1].in_stock"}, Severity: "low"}

	got := Classifier{}.Classify(c, classify.PRContext{DiffHunks: hunks})

	if got.Label != classify.LikelyIntended {
		t.Fatalf("Label = %s, want %s (rationale: %s)", got.Label, classify.LikelyIntended, got.Rationale)
	}
	if got.Confidence != 0.8 {
		t.Fatalf("Confidence = %v, want 0.8", got.Confidence)
	}
	if !strings.Contains(got.Rationale, "InStock") {
		t.Fatalf("rationale should cite the actual diff line, got: %s", got.Rationale)
	}
}

func TestClassify_NoTokenMatch_FallsBackToHeuristic(t *testing.T) {
	// The real shape from jex2l/parity-client-test#3/#5: JSON path leaf
	// "price_cents" vs Go source "Price: 249" — completely different
	// identifiers, no token match possible. Must fall back, not just
	// silently claim grounding where none exists.
	hunks := []classify.DiffHunk{
		{Added: []string{`			{ID: 1, Name: "bolt", Price: 249},`}},
	}
	c := report.ClusterView{Endpoint: "/widgets", ChangedPaths: []string{"$[id=1].price_cents"}, ChangeTypes: []string{"modified"}, Severity: "medium"}

	got := Classifier{Fallback: classify.Heuristic{}}.Classify(c, classify.PRContext{DiffHunks: hunks})

	if got.Label != classify.Uncertain {
		t.Fatalf("Label = %s, want %s (heuristic fallback should still apply)", got.Label, classify.Uncertain)
	}
	if !strings.Contains(got.Rationale, "falling back to structural heuristic") {
		t.Fatalf("rationale should say it fell back, got: %s", got.Rationale)
	}
}

func TestClassify_NoDiffHunksAtAll_FallsBackToHeuristic(t *testing.T) {
	c := report.ClusterView{Endpoint: "/widgets", ChangedPaths: []string{"$.sku"}, ChangeTypes: []string{"FIELD_ADDED"}, Severity: "low"}

	got := Classifier{Fallback: classify.Heuristic{}}.Classify(c, classify.PRContext{})

	// Heuristic's rule 3 (onlyAdditions + low severity) still fires via
	// the fallback, even with zero diff hunks provided.
	if got.Label != classify.LikelyIntended {
		t.Fatalf("Label = %s, want %s via heuristic fallback", got.Label, classify.LikelyIntended)
	}
	if !strings.Contains(got.Rationale, "no diff hunks were available") {
		t.Fatalf("rationale should explain why it fell back, got: %s", got.Rationale)
	}
}

func TestClassify_NoFallbackConfigured_ReturnsUncertain(t *testing.T) {
	c := report.ClusterView{Endpoint: "/widgets", ChangedPaths: []string{"$.sku"}, Severity: "low"}

	got := Classifier{}.Classify(c, classify.PRContext{})

	if got.Label != classify.Uncertain || got.Confidence != 0.0 {
		t.Fatalf("got %+v, want Uncertain/0.0 with no fallback configured", got)
	}
}

func TestClassify_PartialGrounding_UncertainRegardlessOfFallback(t *testing.T) {
	hunks := []classify.DiffHunk{
		{Added: []string{`SKU: "BLT-001",`}},
	}
	c := report.ClusterView{Endpoint: "/widgets", ChangedPaths: []string{"$.sku", "$.weight_kg"}, Severity: "low"}

	got := Classifier{Fallback: classify.Heuristic{}}.Classify(c, classify.PRContext{DiffHunks: hunks})

	if got.Label != classify.Uncertain {
		t.Fatalf("Label = %s, want %s (partial grounding shouldn't fall back or claim full confidence)", got.Label, classify.Uncertain)
	}
	if !strings.Contains(got.Rationale, "weight_kg") {
		t.Fatalf("rationale should name the ungrounded path, got: %s", got.Rationale)
	}
}

func TestClassify_CriticalSeverityErrorAppeared_AlwaysUnintended(t *testing.T) {
	c := report.ClusterView{Endpoint: "/orders", ChangedPaths: []string{"$.status"}, ChangeTypes: []string{"ERROR_APPEARED"}, Severity: "critical"}

	got := Classifier{Fallback: classify.Heuristic{}}.Classify(c, classify.PRContext{})

	if got.Label != classify.LikelyUnintended || got.Confidence != 0.95 {
		t.Fatalf("got %+v, want LikelyUnintended/0.95", got)
	}
}

func TestMentionsField_TokenBoundary_NoFalsePositive(t *testing.T) {
	if mentionsField(`"is_valid": true`, "id") {
		t.Fatalf("mentionsField matched 'id' inside 'is_valid' token — should require a full token match")
	}
	if !mentionsField(`"id": 42`, "id") {
		t.Fatalf("mentionsField failed to match a real whole-token 'id'")
	}
}

func TestLeafField_HandlesBothPathConventions(t *testing.T) {
	cases := map[string]string{
		"$.currency":          "currency",
		"$[id=1].sku":         "sku",
		"$[id=1].price_cents": "price_cents",
		"$.user.address.zip":  "zip",
	}
	for path, want := range cases {
		if got := leafField(path); got != want {
			t.Errorf("leafField(%q) = %q, want %q", path, got, want)
		}
	}
}
