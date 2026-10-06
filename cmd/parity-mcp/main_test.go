// main_test.go proves the MCP server is actually MCP-protocol-compliant
// by connecting a real mcp.Client to it over an in-memory transport (not
// a mock of the protocol) and calling real tool handlers, per
// docs/plans/phase-4.md: this is genuine protocol compliance, just never
// exercised against an external MCP client like Claude Desktop.
//
// Full parity_run_replay-against-a-real-segment coverage already exists
// in eval/harness (via internal/orchestrate directly); this file focuses
// on proving the MCP wiring itself works, using the tools that don't
// require building a recorded segment fixture.
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectInMemory(t *testing.T) *mcp.ClientSession {
	t.Helper()
	server := newServer()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	go func() {
		_, _ = server.Connect(context.Background(), serverTransport, nil)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestMCP_ListTools(t *testing.T) {
	session := connectInMemory(t)
	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{
		"parity_run_replay": false, "parity_get_report": false, "parity_explain_cluster": false,
		"parity_run_regression_tests": false, "parity_ack_cluster": false,
	}
	for _, tool := range res.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestMCP_AckClusterToolEndToEnd(t *testing.T) {
	session := connectInMemory(t)
	ackPath := filepath.Join(t.TempDir(), "acks.json")

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "parity_ack_cluster",
		Arguments: map[string]any{
			"store_path": ackPath, "cluster_key": "orders-field-rename", "verdict": "acknowledged_intended", "reason": "expected",
		},
	})
	if err != nil {
		t.Fatalf("CallTool parity_ack_cluster: %v", err)
	}
	if res.IsError {
		t.Fatalf("parity_ack_cluster returned an error result: %+v", res.Content)
	}
	if _, err := os.Stat(ackPath); err != nil {
		t.Fatalf("expected the ack store file to be created by the real handler, got: %v", err)
	}
}

func TestMCP_GetReportAndExplainClusterEndToEnd(t *testing.T) {
	session := connectInMemory(t)

	reportPath := filepath.Join(t.TempDir(), "report.json")
	body := []byte(`{
		"total_replayed": 10,
		"fidelity": {"score": 1.0, "low_confidence": false},
		"clusters": [{"endpoint": "/orders/{id}", "changed_paths": ["$.status"], "change_types": ["FIELD_REMOVED"], "severity": "high", "count": 2, "weighted_count": 2, "example_exchange_ids": ["ex-1"]}],
		"absorbed_count": 0,
		"signal_count": 2
	}`)
	if err := os.WriteFile(reportPath, body, 0o644); err != nil {
		t.Fatalf("writing test report fixture: %v", err)
	}

	getRes, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "parity_get_report",
		Arguments: map[string]any{"path": reportPath},
	})
	if err != nil {
		t.Fatalf("CallTool parity_get_report: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("parity_get_report returned an error result: %+v", getRes.Content)
	}

	explainRes, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "parity_explain_cluster",
		Arguments: map[string]any{"report_path": reportPath, "cluster_index": 0},
	})
	if err != nil {
		t.Fatalf("CallTool parity_explain_cluster: %v", err)
	}
	if explainRes.IsError {
		t.Fatalf("parity_explain_cluster returned an error result: %+v", explainRes.Content)
	}

	// An out-of-range index must come back as a tool error, not a crash.
	badRes, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "parity_explain_cluster",
		Arguments: map[string]any{"report_path": reportPath, "cluster_index": 99},
	})
	if err != nil {
		t.Fatalf("CallTool parity_explain_cluster (out of range): %v", err)
	}
	if !badRes.IsError {
		t.Fatalf("expected an error result for an out-of-range cluster_index")
	}
}
