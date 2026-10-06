package report

import (
	"fmt"
	"strings"
)

// ClusterAnnotation pairs a cluster with its classification, for
// rendering. Classification is optional (nil before Phase 4's classifier
// runs, or when it's disabled).
type ClusterAnnotation struct {
	Cluster        ClusterView
	Label          string // "LIKELY_INTENDED" / "LIKELY_UNINTENDED" / "UNCERTAIN", or "" if unclassified
	Confidence     float64
	Rationale      string
}

// RenderMarkdown renders the PR-comment body for rep, per spec 6.9: a
// concise summary at top, then a section per cluster (unintended-looking
// first — callers are expected to have already sorted annotations that
// way, mirroring diff.BuildClusters' own severity-first ordering), then
// performance, then generated tests.
func RenderMarkdown(rep Report, annotations []ClusterAnnotation, testsLink string) string {
	var b strings.Builder

	b.WriteString("## Parity / behavioral diff\n\n")
	fmt.Fprintf(&b, "Replayed **%d** requests. Fidelity: **%.0f%%**", rep.TotalReplayed, rep.Fidelity.Score*100)
	if rep.Fidelity.LowConfidence {
		fmt.Fprintf(&b, " ⚠️ *low confidence — %s*", rep.Fidelity.Reason)
	}
	b.WriteString(".\n\n")

	if len(annotations) == 0 {
		b.WriteString("No behavioral differences found beyond what the noise model already absorbs ")
		fmt.Fprintf(&b, "(%d change(s) absorbed as noise).\n", rep.AbsorbedCount)
	} else {
		fmt.Fprintf(&b, "**%d cluster(s)** found (%d signal change(s), %d absorbed as noise).\n\n", len(annotations), rep.SignalCount, rep.AbsorbedCount)
		for i, a := range annotations {
			renderCluster(&b, i+1, a)
		}
	}

	if len(rep.PerfRegressions) > 0 {
		b.WriteString("\n### Performance regressions\n\n")
		b.WriteString("| Endpoint | Base p95 | Candidate p95 | Effect | p-value |\n")
		b.WriteString("|---|---|---|---|---|\n")
		for _, r := range rep.PerfRegressions {
			fmt.Fprintf(&b, "| %s | %.1fms | %.1fms | +%.0f%% | %.2e |\n", r.Endpoint, r.BaseP95Ms, r.CandidateP95Ms, r.EffectPct*100, r.PValue)
		}
	}

	if testsLink != "" {
		fmt.Fprintf(&b, "\n### Generated tests\n\n%s\n", testsLink)
	}

	return b.String()
}

func renderCluster(b *strings.Builder, n int, a ClusterAnnotation) {
	c := a.Cluster
	label := a.Label
	if label == "" {
		label = "UNCLASSIFIED"
	}
	fmt.Fprintf(b, "\n<details>\n<summary>%d. [%s] %s — %s (%d occurrence(s))</summary>\n\n", n, strings.ToUpper(c.Severity), c.Endpoint, label, c.Count)
	fmt.Fprintf(b, "- **Changed paths:** `%s`\n", strings.Join(c.ChangedPaths, "`, `"))
	fmt.Fprintf(b, "- **Change types:** %s\n", strings.Join(c.ChangeTypes, ", "))
	fmt.Fprintf(b, "- **Blast radius:** %.1f weighted request(s)\n", c.WeightedCount)
	if a.Rationale != "" {
		fmt.Fprintf(b, "- **Why %s (%.0f%% confidence):** %s\n", label, a.Confidence*100, a.Rationale)
	}
	if len(c.ExampleExchangeIDs) > 0 {
		fmt.Fprintf(b, "- **Example exchange(s):** `%s`\n", strings.Join(c.ExampleExchangeIDs, "`, `"))
	}
	b.WriteString("\n</details>\n")
}
