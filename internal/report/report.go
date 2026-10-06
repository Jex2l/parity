// Package report defines the machine-readable report schema shared by the
// CLI, (later) the GitHub check run, the MCP server, and the web UI
// (cmd/parity-webui), so all surfaces describe a replay run identically.
package report

import "github.com/jex2l/parity/internal/diff"

// Report is the top-level machine-readable output of a replay run. Its
// JSON shape is the stable contract agents, the PR comment, and the web
// UI all read; see docs/report-schema.json once Phase 4 publishes a
// frozen schema.
type Report struct {
	RunID           string               `json:"run_id"`
	TotalReplayed   int                  `json:"total_replayed"`
	Fidelity        FidelitySummary      `json:"fidelity"`
	Clusters        []ClusterView        `json:"clusters"`
	AbsorbedClusters []ClusterView       `json:"absorbed_clusters,omitempty"`
	AbsorbedCount   int                  `json:"absorbed_count"`
	SignalCount     int                  `json:"signal_count"`
	PerfRegressions []PerfRegressionView `json:"perf_regressions,omitempty"`
	NoiseModel      []NoiseModelEntry    `json:"noise_model,omitempty"`
}

// NoiseModelEntry is one learned (endpoint, JSON path) -> volatility
// classification, exposed so a noise-model inspector can show why a
// path's differences are or aren't trusted as signal.
type NoiseModelEntry struct {
	Endpoint string `json:"endpoint"`
	Path     string `json:"path"`
	Class    string `json:"class"`
}

// PerfRegressionView is the report-facing shape of a perf.Regression.
type PerfRegressionView struct {
	Endpoint      string  `json:"endpoint"`
	BaseP95Ms     float64 `json:"base_p95_ms"`
	CandidateP95Ms float64 `json:"candidate_p95_ms"`
	EffectPct     float64 `json:"effect_pct"`
	PValue        float64 `json:"p_value"`
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
	Examples          []ExampleView `json:"examples,omitempty"`
}

// ExampleView is one concrete exchange backing a cluster: enough to
// render a real before/after comparison in a UI, not just the
// aggregated structural summary.
type ExampleView struct {
	ExchangeID string       `json:"exchange_id"`
	Method     string       `json:"method"`
	Path       string       `json:"path"`
	Changes    []ChangeView `json:"changes"`
	BeforeBody string       `json:"before_body"`
	AfterBody  string       `json:"after_body"`
}

// ChangeView is the report-facing shape of a diff.Change.
type ChangeView struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
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
			Examples:           fromExamples(c.Examples),
		})
	}
	return out
}

func fromExamples(examples []diff.Example) []ExampleView {
	if len(examples) == 0 {
		return nil
	}
	out := make([]ExampleView, 0, len(examples))
	for _, e := range examples {
		changes := make([]ChangeView, 0, len(e.Changes))
		for _, c := range e.Changes {
			changes = append(changes, ChangeView{Path: c.Path, Type: string(c.Type), Before: c.Before, After: c.After})
		}
		out = append(out, ExampleView{
			ExchangeID: e.ExchangeID,
			Method:     e.Method,
			Path:       e.Path,
			Changes:    changes,
			BeforeBody: e.BeforeBody,
			AfterBody:  e.AfterBody,
		})
	}
	return out
}
