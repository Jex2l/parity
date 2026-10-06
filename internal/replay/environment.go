// Package replay provisions base/candidate build environments and runs
// recorded exchanges against them, producing the raw per-exchange results
// that internal/diff turns into a report.
package replay

import "context"

// BuildRef describes how to start one build under test. Parity does not
// build application code itself — callers supply a ready command (for a
// ProcessEnvironment) or image reference (for a future container-based
// Environment); see docs/decisions for why Docker-based environments are
// not implemented yet in this repo.
type BuildRef struct {
	Name string            // human-readable label: "base" / "candidate"
	Cmd  string            // executable to run
	Args []string
	Env  map[string]string // additional environment variables for the subprocess
}

// Endpoint is where a provisioned environment can be reached.
type Endpoint struct {
	BaseURL string
}

// Environment provisions, resets, and tears down an isolated instance of
// a build under test. Implementations must be safe to Teardown even if
// Provision partially failed.
type Environment interface {
	Provision(ctx context.Context, build BuildRef) (Endpoint, error)
	Reset(ctx context.Context) error
	Teardown(ctx context.Context) error
}
