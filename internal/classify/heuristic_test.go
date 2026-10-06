package classify

import (
	"testing"

	"github.com/jex2l/parity/internal/report"
)

func TestHeuristic_StatusAppearedIsAlwaysUnintended(t *testing.T) {
	c := report.ClusterView{
		Endpoint:    "/orders/{id}",
		ChangeTypes: []string{"ERROR_APPEARED", "STATUS_CHANGED"},
		Severity:    "critical",
	}
	res := Heuristic{}.Classify(c, PRContext{ChangedFiles: []string{"internal/orders/handler.go"}})
	if res.Label != LikelyUnintended {
		t.Fatalf("expected LIKELY_UNINTENDED for a new 5xx, got %+v", res)
	}
	if res.Confidence < 0.9 {
		t.Fatalf("expected high confidence for a 5xx regression, got %v", res.Confidence)
	}
}

func TestHeuristic_RemovalInUnrelatedCodeIsUnintended(t *testing.T) {
	c := report.ClusterView{
		Endpoint:    "/orders/{id}",
		ChangeTypes: []string{"FIELD_REMOVED", "FIELD_ADDED"},
		Severity:    "high",
	}
	pr := PRContext{ChangedFiles: []string{"internal/users/handler.go", "README.md"}}
	res := Heuristic{}.Classify(c, pr)
	if res.Label != LikelyUnintended {
		t.Fatalf("expected LIKELY_UNINTENDED for a field rename in unrelated code, got %+v", res)
	}
}

func TestHeuristic_RemovalInRelatedCodeIsIntended(t *testing.T) {
	c := report.ClusterView{
		Endpoint:    "/orders/{id}",
		ChangeTypes: []string{"FIELD_REMOVED", "FIELD_ADDED"},
		Severity:    "high",
	}
	pr := PRContext{ChangedFiles: []string{"internal/orders/handler.go"}, Title: "Rename order status to state"}
	res := Heuristic{}.Classify(c, pr)
	if res.Label != LikelyIntended {
		t.Fatalf("expected LIKELY_INTENDED when the PR touches related code, got %+v", res)
	}
}

func TestHeuristic_AdditiveChangeInRelatedCodeIsIntended(t *testing.T) {
	c := report.ClusterView{
		Endpoint:    "/orders/{id}",
		ChangeTypes: []string{"FIELD_ADDED"},
		Severity:    "low",
	}
	pr := PRContext{ChangedFiles: []string{"internal/orders/handler.go"}}
	res := Heuristic{}.Classify(c, pr)
	if res.Label != LikelyIntended {
		t.Fatalf("expected LIKELY_INTENDED for an additive change in related code, got %+v", res)
	}
}

func TestHeuristic_UnexplainedValueChangeIsUncertain(t *testing.T) {
	c := report.ClusterView{
		Endpoint:    "/orders/{id}",
		ChangeTypes: []string{"VALUE_CHANGED"},
		Severity:    "medium",
	}
	pr := PRContext{ChangedFiles: []string{"internal/billing/tax.go"}}
	res := Heuristic{}.Classify(c, pr)
	if res.Label != Uncertain {
		t.Fatalf("expected UNCERTAIN for an unexplained value change, got %+v", res)
	}
}

// TestHeuristic_MutationCatalog mirrors the Phase 4 acceptance criterion:
// run the heuristic against the shape of each Phase 2/3 mutation's real
// cluster output and check it lands on the right side.
func TestHeuristic_MutationCatalog(t *testing.T) {
	cases := []struct {
		name    string
		cluster report.ClusterView
		pr      PRContext
		want    Label
	}{
		{
			name:    "status_5xx",
			cluster: report.ClusterView{Endpoint: "/orders/{id}", ChangeTypes: []string{"ERROR_APPEARED", "STATUS_CHANGED", "FIELD_ADDED", "FIELD_REMOVED"}, Severity: "critical"},
			pr:      PRContext{ChangedFiles: []string{"internal/orders/handler.go"}},
			want:    LikelyUnintended,
		},
		{
			name:    "field_rename_unrelated_pr",
			cluster: report.ClusterView{Endpoint: "/orders/{id}", ChangeTypes: []string{"FIELD_ADDED", "FIELD_REMOVED"}, Severity: "high"},
			pr:      PRContext{ChangedFiles: []string{"internal/payments/stripe.go"}},
			want:    LikelyUnintended,
		},
		{
			name:    "added_field_intended",
			cluster: report.ClusterView{Endpoint: "/orders/{id}", ChangeTypes: []string{"FIELD_ADDED"}, Severity: "low"},
			pr:      PRContext{ChangedFiles: []string{"internal/orders/handler.go"}, Title: "Add currency field to orders"},
			want:    LikelyIntended,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Heuristic{}.Classify(tc.cluster, tc.pr)
			if res.Label != tc.want {
				t.Fatalf("%s: got %s (%.2f, %q), want %s", tc.name, res.Label, res.Confidence, res.Rationale, tc.want)
			}
		})
	}
}
