// Command parity is the user-facing CLI. Phase 1 implements `parity
// inspect` only; `replay`, `report`, `test`, `ack` land in later phases
// per docs/plans.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jex2l/parity/internal/recording"
)

// Exit codes per the agent-integration contract (section 6.10 of the spec).
const (
	exitClean      = 0
	exitInfraError = 30
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitInfraError)
	}
	switch os.Args[1] {
	case "inspect":
		os.Exit(runInspect(os.Args[2:]))
	default:
		usage()
		os.Exit(exitInfraError)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: parity inspect <segment-path> [--jsonl]")
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
