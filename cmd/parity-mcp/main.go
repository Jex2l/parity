// Command parity-mcp exposes Parity to coding agents over MCP (spec
// 6.10). Tools return compact, structured output designed to fit in an
// agent's context window — summaries first, details on demand.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jex2l/parity/internal/feedback"
	orchestrate "github.com/jex2l/parity/internal/orchestrate"
	"github.com/jex2l/parity/internal/recording"
	"github.com/jex2l/parity/internal/replay"
	"github.com/jex2l/parity/internal/report"
	"github.com/jex2l/parity/internal/scrub"
	"github.com/jex2l/parity/internal/testgen"
)

func main() {
	server := newServer()
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("parity-mcp: %v", err)
	}
}

// newServer builds the MCP server with every tool registered. Factored
// out of main so tests can connect to it via an in-memory transport
// instead of stdio.
func newServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "parity", Version: "0.1.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "parity_run_replay",
		Description: "Run a three-way (base-A/base-B/candidate) replay of a recorded segment and return a compact report summary.",
	}, runReplayTool)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "parity_get_report",
		Description: "Fetch a previously written report.json by path.",
	}, getReportTool)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "parity_explain_cluster",
		Description: "Get full detail (changed paths, examples, severity) for one cluster by index in a report.json.",
	}, explainClusterTool)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "parity_run_regression_tests",
		Description: "Run a generated portable test suite (cases.json) against a running build command.",
	}, runRegressionTestsTool)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "parity_ack_cluster",
		Description: "Mark a cluster (by its stable key) as acknowledged-intended or a confirmed bug, to suppress/flag it in future runs.",
	}, ackClusterTool)

	return s
}

// --- parity_run_replay ---

type runReplayArgs struct {
	Segment        string            `json:"segment" jsonschema:"path to a recorded segment file"`
	BaseCmd        string            `json:"base_cmd" jsonschema:"executable to run as the base build"`
	CandidateCmd   string            `json:"candidate_cmd" jsonschema:"executable to run as the candidate build"`
	CandidateEnv   map[string]string `json:"candidate_env,omitempty" jsonschema:"extra environment variables for the candidate process"`
	Sample         int               `json:"sample,omitempty" jsonschema:"max number of exchanges to replay; 0 means all"`
	PortEnv        string            `json:"port_env,omitempty" jsonschema:"env var the build reads its listen port from; default DEMO_ADDR"`
	HealthPath     string            `json:"health_path,omitempty" jsonschema:"path polled to detect readiness; default /health"`
	// HMACSecretEnv names an environment variable holding the same
	// pseudonymization secret `parity-capture` used to produce Segment.
	// If the segment came from a real capture (PII-scrubbed), omitting
	// this makes every PII-bearing field look like a spurious diff — see
	// docs/limits.md's robustness-pass entry on this.
	HMACSecretEnv string `json:"hmac_secret_env,omitempty" jsonschema:"env var holding the HMAC secret parity-capture used to scrub this segment, if any"`
}

type runReplayOutput struct {
	TotalReplayed int                           `json:"total_replayed"`
	Fidelity      report.FidelitySummary        `json:"fidelity"`
	ClusterCount  int                           `json:"cluster_count"`
	Clusters      []report.ClusterView          `json:"clusters"`
	PerfRegressions []report.PerfRegressionView `json:"perf_regressions,omitempty"`
}

func runReplayTool(ctx context.Context, _ *mcp.CallToolRequest, args runReplayArgs) (*mcp.CallToolResult, runReplayOutput, error) {
	portEnv, healthPath := orDefault(args.PortEnv, "DEMO_ADDR"), orDefault(args.HealthPath, "/health")

	r, err := recording.NewReader(args.Segment)
	if err != nil {
		return nil, runReplayOutput{}, err
	}
	defer r.Close()
	exchanges, err := orchestrate.LoadExchanges(r.Next, args.Sample)
	if err != nil {
		return nil, runReplayOutput{}, err
	}

	cfg := orchestrate.Config{
		Base:      replay.BuildRef{Name: "base", Cmd: args.BaseCmd},
		Candidate: replay.BuildRef{Name: "candidate", Cmd: args.CandidateCmd, Env: args.CandidateEnv},
		NewEnv: func() replay.Environment {
			return replay.NewProcessEnvironment(portEnv, healthPath)
		},
		Concurrency: 8,
	}
	if args.HMACSecretEnv != "" {
		if secret := os.Getenv(args.HMACSecretEnv); secret != "" {
			cfg.Scrubber = scrub.New(scrub.Config{HMACSecret: []byte(secret)})
		}
	}
	rep, err := orchestrate.Run(ctx, cfg, exchanges)
	if err != nil {
		return nil, runReplayOutput{}, err
	}

	out := runReplayOutput{
		TotalReplayed:   rep.TotalReplayed,
		Fidelity:        rep.Fidelity,
		ClusterCount:    len(rep.Clusters),
		Clusters:        rep.Clusters,
		PerfRegressions: rep.PerfRegressions,
	}
	return nil, out, nil
}

// --- parity_get_report ---

type getReportArgs struct {
	Path string `json:"path" jsonschema:"path to a report.json written by parity replay or parity_run_replay"`
}

func getReportTool(_ context.Context, _ *mcp.CallToolRequest, args getReportArgs) (*mcp.CallToolResult, report.Report, error) {
	b, err := os.ReadFile(args.Path)
	if err != nil {
		return textError[report.Report](err)
	}
	var rep report.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return textError[report.Report](err)
	}
	return nil, rep, nil
}

// --- parity_explain_cluster ---

type explainClusterArgs struct {
	ReportPath   string `json:"report_path"`
	ClusterIndex int    `json:"cluster_index" jsonschema:"0-based index into the report's clusters array"`
}

func explainClusterTool(_ context.Context, _ *mcp.CallToolRequest, args explainClusterArgs) (*mcp.CallToolResult, report.ClusterView, error) {
	b, err := os.ReadFile(args.ReportPath)
	if err != nil {
		return textError[report.ClusterView](err)
	}
	var rep report.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return textError[report.ClusterView](err)
	}
	if args.ClusterIndex < 0 || args.ClusterIndex >= len(rep.Clusters) {
		return textError[report.ClusterView](fmt.Errorf("cluster_index %d out of range (report has %d clusters)", args.ClusterIndex, len(rep.Clusters)))
	}
	return nil, rep.Clusters[args.ClusterIndex], nil
}

// --- parity_run_regression_tests ---

type runRegressionTestsArgs struct {
	CasesPath  string `json:"cases_path"`
	Cmd        string `json:"cmd"`
	PortEnv    string `json:"port_env,omitempty"`
	HealthPath string `json:"health_path,omitempty"`
}

type regressionTestsOutput struct {
	Passed int      `json:"passed"`
	Failed int      `json:"failed"`
	Failures []string `json:"failures,omitempty"`
}

func runRegressionTestsTool(ctx context.Context, _ *mcp.CallToolRequest, args runRegressionTestsArgs) (*mcp.CallToolResult, regressionTestsOutput, error) {
	portEnv, healthPath := orDefault(args.PortEnv, "DEMO_ADDR"), orDefault(args.HealthPath, "/health")

	b, err := os.ReadFile(args.CasesPath)
	if err != nil {
		return textError[regressionTestsOutput](err)
	}
	var cases []testgen.Case
	if err := json.Unmarshal(b, &cases); err != nil {
		return textError[regressionTestsOutput](err)
	}

	env := replay.NewProcessEnvironment(portEnv, healthPath)
	ep, err := env.Provision(ctx, replay.BuildRef{Name: "mcp-test-run", Cmd: args.Cmd})
	if err != nil {
		return textError[regressionTestsOutput](err)
	}
	defer env.Teardown(ctx)

	results := testgen.Run(ep.BaseURL, cases)
	out := regressionTestsOutput{}
	for _, r := range results {
		if r.Passed {
			out.Passed++
		} else {
			out.Failed++
			out.Failures = append(out.Failures, fmt.Sprintf("%s: %s", r.Case.Name, r.Reason))
		}
	}
	return nil, out, nil
}

// --- parity_ack_cluster ---

type ackClusterArgs struct {
	StorePath  string `json:"store_path" jsonschema:"path to the ack store JSON file (created if missing)"`
	ClusterKey string `json:"cluster_key"`
	Verdict    string `json:"verdict" jsonschema:"'acknowledged_intended' or 'bug'"`
	Reason     string `json:"reason,omitempty"`
}

type ackClusterOutput struct {
	OK bool `json:"ok"`
}

func ackClusterTool(_ context.Context, _ *mcp.CallToolRequest, args ackClusterArgs) (*mcp.CallToolResult, ackClusterOutput, error) {
	store, err := feedback.Open(args.StorePath)
	if err != nil {
		return textError[ackClusterOutput](err)
	}
	verdict := feedback.Verdict(args.Verdict)
	if verdict != feedback.VerdictAcknowledgedIntended && verdict != feedback.VerdictBug {
		return textError[ackClusterOutput](fmt.Errorf("verdict must be %q or %q, got %q", feedback.VerdictAcknowledgedIntended, feedback.VerdictBug, args.Verdict))
	}
	if err := store.Record(args.ClusterKey, verdict, args.Reason); err != nil {
		return textError[ackClusterOutput](err)
	}
	return nil, ackClusterOutput{OK: true}, nil
}

// --- helpers ---

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// textError returns a tool error in the shape the generic AddTool handler
// expects (nil result, zero output, non-nil error) — kept as a named
// helper so every tool reports errors identically.
func textError[T any](err error) (*mcp.CallToolResult, T, error) {
	var zero T
	return nil, zero, err
}
