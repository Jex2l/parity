// Command parity is the user-facing CLI. `inspect` (Phase 1) and
// `replay`/`report` (Phase 2) are implemented; `test`, `ack` land in
// later phases per docs/plans.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jex2l/parity/internal/recording"
	"github.com/jex2l/parity/internal/replay"

	orchestrate "github.com/jex2l/parity/internal/orchestrate"
	"github.com/jex2l/parity/internal/report"
)

// Exit codes per the agent-integration contract (section 6.10 of the spec).
const (
	exitClean         = 0
	exitUnintended    = 10
	exitFidelityLow   = 20
	exitInfraError    = 30
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitInfraError)
	}
	switch os.Args[1] {
	case "inspect":
		os.Exit(runInspect(os.Args[2:]))
	case "replay":
		os.Exit(runReplay(os.Args[2:]))
	case "report":
		os.Exit(runReport(os.Args[2:]))
	default:
		usage()
		os.Exit(exitInfraError)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  parity inspect <segment-path> [--jsonl]
  parity replay --segment <path> --base-cmd <path> --candidate-cmd <path> [--candidate-env K=V] [--sample N] [--out report.json]
  parity report <report.json>`)
}

func runInspect(args []string) int {
	if len(args) < 1 {
		usage()
		return exitInfraError
	}
	path := args[0]
	toJSONL := false
	for _, a := range args[1:] {
		if a == "--jsonl" {
			toJSONL = true
		}
	}

	if toJSONL {
		out := path + ".jsonl"
		n, err := recording.ExportJSONL(path, out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parity inspect: %v\n", err)
			return exitInfraError
		}
		fmt.Printf("wrote %d records to %s\n", n, out)
		return exitClean
	}

	idx, err := recording.ReadIndex(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity inspect: %v\n", err)
		return exitInfraError
	}

	byEndpoint := map[string]int{}
	byStatus := map[int32]int{}
	for _, e := range idx {
		byEndpoint[e.EndpointTmpl]++
		byStatus[e.Status]++
	}

	summary := map[string]any{
		"segment":       path,
		"record_count":  len(idx),
		"by_endpoint":   byEndpoint,
		"by_status":     byStatus,
	}
	enc := json.NewEncoder(printWriter(os.Stdout))
	enc.SetIndent("", "  ")
	enc.Encode(summary)
	return exitClean
}

func printWriter(w io.Writer) io.Writer { return w }

func runReplay(args []string) int {
	var segment, baseCmd, candidateCmd, out, portEnv, healthPath string
	var sample, concurrency int
	candidateEnv := map[string]string{}

	portEnv, healthPath = "DEMO_ADDR", "/health"
	out = "report.json"
	concurrency = 8

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--segment":
			i++
			segment = args[i]
		case "--base-cmd":
			i++
			baseCmd = args[i]
		case "--candidate-cmd":
			i++
			candidateCmd = args[i]
		case "--candidate-env":
			i++
			kv := splitKV(args[i])
			if kv[0] != "" {
				candidateEnv[kv[0]] = kv[1]
			}
		case "--sample":
			i++
			fmt.Sscanf(args[i], "%d", &sample)
		case "--concurrency":
			i++
			fmt.Sscanf(args[i], "%d", &concurrency)
		case "--out":
			i++
			out = args[i]
		case "--port-env":
			i++
			portEnv = args[i]
		case "--health-path":
			i++
			healthPath = args[i]
		}
	}
	if segment == "" || baseCmd == "" || candidateCmd == "" {
		usage()
		return exitInfraError
	}

	r, err := recording.NewReader(segment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity replay: %v\n", err)
		return exitInfraError
	}
	defer r.Close()
	exchanges, err := orchestrate.LoadExchanges(r.Next, sample)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity replay: %v\n", err)
		return exitInfraError
	}
	if len(exchanges) == 0 {
		fmt.Fprintln(os.Stderr, "parity replay: segment has no exchanges to replay")
		return exitInfraError
	}

	cfg := orchestrate.Config{
		Base:      replay.BuildRef{Name: "base", Cmd: baseCmd},
		Candidate: replay.BuildRef{Name: "candidate", Cmd: candidateCmd, Env: candidateEnv},
		NewEnv: func() replay.Environment {
			return replay.NewProcessEnvironment(portEnv, healthPath)
		},
		Concurrency: concurrency,
	}

	rep, err := orchestrate.Run(context.Background(), cfg, exchanges)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity replay: %v\n", err)
		return exitInfraError
	}

	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity replay: %v\n", err)
		return exitInfraError
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	encErr := enc.Encode(rep)
	f.Close()
	if encErr != nil {
		fmt.Fprintf(os.Stderr, "parity replay: writing report: %v\n", encErr)
		return exitInfraError
	}

	printReportSummary(rep)
	fmt.Printf("full report written to %s\n", out)

	if rep.Fidelity.LowConfidence {
		return exitFidelityLow
	}
	if len(rep.Clusters) > 0 {
		return exitUnintended
	}
	return exitClean
}

func runReport(args []string) int {
	if len(args) < 1 {
		usage()
		return exitInfraError
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity report: %v\n", err)
		return exitInfraError
	}
	var rep report.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		fmt.Fprintf(os.Stderr, "parity report: %v\n", err)
		return exitInfraError
	}
	printReportSummary(rep)
	return exitClean
}

func printReportSummary(rep report.Report) {
	fmt.Printf("replayed %d exchanges, fidelity=%.2f", rep.TotalReplayed, rep.Fidelity.Score)
	if rep.Fidelity.LowConfidence {
		fmt.Printf(" (LOW CONFIDENCE: %s)", rep.Fidelity.Reason)
	}
	fmt.Println()
	fmt.Printf("%d cluster(s), %d signal change(s), %d absorbed as noise\n", len(rep.Clusters), rep.SignalCount, rep.AbsorbedCount)
	for _, c := range rep.Clusters {
		fmt.Printf("  [%s] %s  paths=%v  count=%d (weighted %.1f)\n", c.Severity, c.Endpoint, c.ChangedPaths, c.Count, c.WeightedCount)
	}
}

func splitKV(s string) [2]string {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return [2]string{s[:i], s[i+1:]}
		}
	}
	return [2]string{"", ""}
}
