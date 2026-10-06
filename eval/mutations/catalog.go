// Package mutations is the ground-truth catalog of injected regressions
// (and non-regressions) used to score the diff engine, per product spec
// section 7(3)/(4). Each entry names a PARITY_MUTATION value the demo
// service understands (see eval/demo-service/main.go) and whether a
// correct Parity report should flag it.
package mutations

// Expectation is the ground truth for one mutation.
type Expectation string

const (
	ExpectUnintended Expectation = "unintended" // should produce at least one signal cluster
	ExpectIntendedOrClean Expectation = "intended_or_clean" // should NOT be hidden, but also must not be mistaken for a crash; Phase 2 only checks it doesn't regress fidelity/crash — intent classification is Phase 4
	ExpectNoOp      Expectation = "no_op" // should produce zero signal clusters
)

// Mutation is one catalog entry.
type Mutation struct {
	Name        string
	EnvValue    string // PARITY_MUTATION value; "" means the unmutated baseline
	Expect      Expectation
	Description string
}

// Catalog lists every mutation Phase 2's eval harness scores against.
// This is intentionally smaller than the full spec's catalog (field
// rename, type change, off-by-one, rounding, 5xx, N+1, slow query, auth
// regression, etc. — ~15+ entries) — Phase 2 builds enough to prove the
// three-way comparison and scoring harness work end to end; growing the
// catalog toward the full list is explicit follow-up work (docs/limits.md),
// not something to fake by inflating this list without matching demo
// service behavior.
var Catalog = []Mutation{
	{
		Name:        "no_op_baseline",
		EnvValue:    "",
		Expect:      ExpectNoOp,
		Description: "Candidate identical to base; must produce zero signal clusters.",
	},
	{
		Name:        "status_5xx",
		EnvValue:    "status_5xx",
		Expect:      ExpectUnintended,
		Description: "GET /orders/{id} always returns 500 — critical, must be detected.",
	},
	{
		Name:        "field_rename",
		EnvValue:    "field_rename",
		Expect:      ExpectUnintended,
		Description: "order.status renamed to order.state with no deprecation — FIELD_REMOVED + FIELD_ADDED.",
	},
	{
		Name:        "rounding_bug",
		EnvValue:    "rounding_bug",
		Expect:      ExpectUnintended,
		Description: "order.total truncated to whole dollars — VALUE_CHANGED beyond float-jitter tolerance.",
	},
	{
		Name:        "pagination_off_by_one",
		EnvValue:    "pagination_off_by_one",
		Expect:      ExpectUnintended,
		Description: "GET /users pagination skips the first item of each page — ARRAY changes on /users.",
	},
	{
		Name:        "added_field_intended",
		EnvValue:    "added_field_intended",
		Expect:      ExpectIntendedOrClean,
		Description: "order gains a new 'currency' field — FIELD_ADDED only, low severity, not a crash.",
	},
}
