// Package report defines the machine-readable report schema shared by the
// CLI, (later) the GitHub check run, and the MCP server, so all surfaces
// describe a replay run identically.
package report

import "github.com/jex2l/parity/internal/diff"

// Report is the top-level machine-readable output of a replay run. Its
// JSON shape is the stable contract agents and the PR comment both read;
// see docs/report-schema.json once Phase 4 publishes a frozen schema.
type Report struct {
	RunID          string         `json:"run_id"`
	TotalReplayed  int            `json:"total_replayed"`
	Fidelity       FidelitySummary `json:"fidelity"`
	Clusters       []ClusterView  `json:"clusters"`
	AbsorbedCount  int            `json:"absorbed_count"`
	SignalCount    int            `json:"signal_count"`
}

type FidelitySummary struct {
	Score        float64 `json:"score"`
	LowConfidence bool   `json:"low_confidence"`
	Reason       string  `json:"reason,omitempty"`
}

type ClusterView struct {
	Endpoint          string   `json:"endpoint"`
	ChangedPaths      []string `json:"changed_paths"`
	ChangeTypes       []string `json:"change_types"`
	Severity          string   `json:"severity"`
	Count             int      `json:"count"`
	WeightedCount     float64  `json:"weighted_count"`
	ExampleExchangeIDs []string `json:"example_exchange_ids"`
}

// FromClusters converts diff.Cluster results into the report's view type.
func FromClusters(clusters []diff.Cluster) []ClusterView {
	out := make([]ClusterView, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, ClusterView{
			Endpoint:           c.Endpoint,
			ChangedPaths:       c.ChangedPaths,
			ChangeTypes:        c.ChangeTypes,
			Severity:           c.Severity.String(),
			Count:              c.Count,
			WeightedCount:      c.WeightedCount,
			ExampleExchangeIDs: c.ExampleExchangeIDs,
		})
	}
	return out
}
