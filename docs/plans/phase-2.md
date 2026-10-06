# Phase 2 — Replay and three-way comparison

## Goal
Replay a recorded segment against a base and a candidate build, run the
three-way comparison (base-A vs base-B gives the noise floor; base-A vs
candidate gives signal), cluster the differences, score severity, compute
a fidelity score, and report via `parity replay`/`parity report`.

## Scope decisions (deviations from the full spec, documented here instead
of silently narrowing)

- **Environment driver.** The spec's default is Docker Compose/Kubernetes.
  Docker is unavailable in this dev environment (ADR 0002). Phase 2 adds a
  `replay.Environment` interface and a `ProcessEnvironment` implementation
  that starts a build command as a local subprocess bound to a free port.
  This is enough to prove the replay scheduler and diff engine end to end;
  a `DockerComposeEnvironment` satisfying the same interface is Phase 2.5
  work gated on a Docker daemon being available to actually test it
  against (do not claim it works without running it).
- **Base vs candidate builds for the eval harness.** Parity itself never
  builds application code — callers hand it a build command or image ref.
  For the eval harness (not for real usage), the demo service accepts a
  `PARITY_MUTATION` env var that toggles one injected behavior change at a
  time, so "base" and "candidate" are the same binary with the env var
  unset/set respectively. This is a harness convenience to get many
  labeled mutations cheaply, not how `parity replay` works for a real
  customer (who passes two real build refs/images). Documented so it's
  never confused with the real interface.
- **Outbound dependency stubbing.** The demo service has no outbound
  dependencies yet, so `internal/stub` is built and unit-tested against
  synthetic recorded calls, not exercised by the demo/eval walkthrough
  this phase. Wiring a downstream-calling endpoint into the demo service is
  deferred to Phase 3 (where N+1 detection needs it anyway).
- **Database state / auth injection** are not implemented this phase; the
  demo service is stateless and unauthenticated, so three-way comparison
  doesn't need them yet. Tracked in docs/limits.md.

## Files
- `internal/normalize/` — JSON canonicalization, value-flow placeholder
  normalization, per-path noise classification (STABLE / TIMESTAMP_LIKE /
  ALWAYS_DIFFERENT_OPAQUE / ORDER_UNSTABLE / FLOAT_JITTER / MONOTONIC_COUNTER)
- `internal/diff/` — structural diff (typed changes at JSON paths),
  severity scoring, clustering, fidelity score
- `internal/stub/` — outbound call recorder/matcher (record + serve)
- `internal/replay/` — `Environment` interface, `ProcessEnvironment`,
  replay `Scheduler` (concurrency-bounded, replays a segment against an
  endpoint and collects response exchanges)
- `cmd/parity`: add `replay` and `report` subcommands, JSON report schema
- `eval/mutations/` — mutation catalog toggled via `PARITY_MUTATION`
- `eval/harness/mutation_test.go` — scoring: detection recall on unintended
  mutations, false-positive rate on a no-op "mutation"

## Accept
On the eval harness with the mutation catalog built this phase, detect
>=80% of unintended regressions and report zero diff clusters on the no-op
case, matching the Phase 2 acceptance criterion in the original spec
(scaled to however many mutations actually get built this phase — stated
honestly in the phase summary, not inflated).

## Phase 2 summary (what actually happened)

**Built and verified:** `internal/normalize` (shape classifiers),
`internal/diff` (structural diff, noise model via A/B learning, severity
scoring, clustering, fidelity scoring — all unit tested, including
diff(a,a)=empty and noise-absorption cases), `internal/replay`
(`Environment` interface + `ProcessEnvironment`, concurrent `Scheduler`),
`internal/orchestrate` (three-way comparison tying replay+diff together),
`internal/report` (JSON report schema), and `cmd/parity replay`/`report`.

**Eval harness result (real, run, not estimated):** 6-entry mutation
catalog, **100% detection recall (4/4 unintended mutations)**, **0 false
positives** on the no-op case, fidelity 1.00 throughout — exceeds the 80%
acceptance target. See `eval/harness/mutation_test.go`.

**Found and documented, not hidden:** manually running `parity replay`
against a real capture-produced segment (from `make demo`) surfaced that
PII-scrubbed recorded responses don't match raw replay responses,
collapsing the fidelity score for reasons unrelated to the candidate
build. This is now `docs/limits.md`'s top Phase 2 limitation, with the
real fix identified (consistent pseudonymization on both sides) and
explicitly deferred, not patched over by weakening the eval harness itself
— the harness avoids the issue by recording its own baseline unscrubbed,
which is a legitimate harness simplification but means this gap would
still bite a real user running `parity replay` on a real segment today.

**Deferred (listed in docs/limits.md):** Docker/Kubernetes environments,
outbound dependency stubbing, DB snapshot restore, auth credential
injection, cross-run noise model persistence, the full ~15-entry mutation
catalog.
