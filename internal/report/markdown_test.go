package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleReport() Report {
	return Report{
		TotalReplayed: 500,
		Fidelity:      FidelitySummary{Score: 0.97},
		SignalCount:   3,
		AbsorbedCount: 42,
		PerfRegressions: []PerfRegressionView{
			{Endpoint: "/slow", BaseP95Ms: 50.2, CandidateP95Ms: 253.8, EffectPct: 4.05, PValue: 6.3e-08},
		},
	}
}

func sampleAnnotations() []ClusterAnnotation {
	return []ClusterAnnotation{
		{
			Cluster: ClusterView{
				Endpoint: "/orders/{id}", ChangedPaths: []string{"$.status"}, ChangeTypes: []string{"FIELD_REMOVED", "FIELD_ADDED"},
				Severity: "high", Count: 5, WeightedCount: 5, ExampleExchangeIDs: []string{"ex-1", "ex-2"},
			},
			Label: "LIKELY_UNINTENDED", Confidence: 0.8, Rationale: "a field was removed in code this PR did not touch",
		},
	}
}

func TestRenderMarkdown_Golden(t *testing.T) {
	got := RenderMarkdown(sampleReport(), sampleAnnotations(), "https://example.invalid/tests/run-123")

	goldenPath := filepath.Join("testdata", "report.golden.md")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden file: %v", err)
		}
		t.Logf("wrote golden file %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden file (run with UPDATE_GOLDEN=1 to create it): %v", err)
	}
	if got != string(want) {
		t.Fatalf("rendered markdown does not match golden file.\n--- got ---\n%s\n--- want ---\n%s", got, string(want))
	}
}

func TestRenderMarkdown_NoClustersIsClean(t *testing.T) {
	rep := Report{TotalReplayed: 100, Fidelity: FidelitySummary{Score: 1.0}, AbsorbedCount: 12}
	got := RenderMarkdown(rep, nil, "")
	if !containsAll(got, "No behavioral differences found", "12 change(s) absorbed as noise") {
		t.Fatalf("expected a clean no-diff message, got: %s", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
