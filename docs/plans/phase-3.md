# Phase 3 — Performance analysis and test generation

## Goal
Detect latency regressions using the base-A/base-B run as a noise floor
(not raw candidate-vs-base, which would flag normal variance), and distill
a minimal diverse set of recorded exchanges into a portable, replayable
test format.

## Scope decisions
- **Significance test.** The spec calls for Mann-Whitney U plus bootstrap
  CIs. This phase implements Mann-Whitney U with a normal approximation
  for the p-value (standard for n>=20; the harness always has more samples
  than that) rather than an exact permutation test, which is adequate for
  v1 and documented as such rather than silently simplified.
- **Resource metrics (CPU/memory via cgroup/Prometheus) and N+1 detection
  via downstream call counts are deferred.** The demo service has no
  outbound dependencies yet (Phase 2 limit, still true), so there is
  nothing to count; building the counting logic without anything real to
  count against would be unverifiable. Tracked in docs/limits.md.
- **Test generation emits the portable YAML format only** (no
  language-native Go/Python/TS templates this phase).

## Files
- `internal/perf/latency.go` — percentiles, Mann-Whitney U, regression
  detection gated on both statistical significance and minimum effect size
  exceeding the A-vs-B noise floor
- `internal/perf/latency_test.go`
- `internal/testgen/testgen.go` — minimal diverse exchange selection +
  portable YAML test case format with noise-aware matchers
  (`<ANY_UUID>`, `<ANY_TIMESTAMP>`)
- `internal/testgen/testgen_test.go`
- `cmd/parity`: add `test generate` and `test run` subcommands
- `eval/demo-service`: `slow_5x` mutation (5x sleep on `/slow`)
- `eval/harness/perf_test.go` — detect the injected 5x slowdown with zero
  false positives on the no-op case

## Accept
Detect the injected 5x slowdown on `/slow` with no false positive on the
no-op mutation; generated tests for the mutation catalog's `status_5xx`
and `field_rename` cases fail against the mutated candidate and pass
against the unmutated base (proving they actually catch what they were
distilled from, not just that they run).

## Phase 3 summary (what actually happened)

**Built and verified:** `internal/perf` (percentiles, Mann-Whitney U with
normal approximation, noise-floor-gated regression detection — unit
tested including an explicit "within noise floor, don't flag" case) and
`internal/testgen` (minimal diverse case selection, UUID/timestamp
masking, portable JSON test format, an executor). Wired perf regression
detection into `internal/orchestrate` and added `PerfRegressions` to the
report schema. Added `parity test generate`/`parity test run` CLI
subcommands.

**Eval harness results (real, run):**
- `eval/harness/perf_test.go`: injected 5x slowdown on `/slow` detected
  at p=6.3e-08 with effect size ~371%; **zero false positives** on the
  no-op candidate.
- `eval/harness/testgen_test.go`: tests generated from unmutated recordings
  pass against the unmutated base, and **fail** against both
  `field_rename` and `status_5xx` mutations — proving generated tests
  catch what they were distilled from, the actual Phase 3 acceptance bar,
  not just "the test runner doesn't crash."

**Found and fixed during this phase, not hidden:** `go vet` caught a real
bug in a draft version of the perf eval test — copying a `*pb.Exchange`
by value also copies protobuf's internal `sync.Mutex`, which is unsafe.
Fixed by constructing a fresh `Exchange` instead of a shallow copy; `go
vet ./...` is clean.

**Deferred (listed in docs/limits.md):** resource metrics / N+1 detection
(nothing to measure yet — no outbound deps modeled), language-native test
generators, full noise-class-aware masking in testgen (only UUID/timestamp
today, not the float-jitter/order-unstable classes `internal/diff` knows
about).
