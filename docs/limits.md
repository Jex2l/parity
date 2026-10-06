# Known limits

This document is meant to be read before trusting a Parity report. Updated
as each phase lands; entries are never deleted quietly — if a limit is
fixed, it moves to "resolved" with the phase that fixed it, so the history
of what was once true stays visible.

## Inherent limits of behavioral replay (apply from Phase 2 onward)

- **Stateful / write-heavy flows.** Replaying a request that mutates state
  against a snapshot-restored database is only faithful if nothing else in
  the replay batch depends on that mutation. Phase 1–5 handle this via
  snapshot restore + optional isolation per write request; true
  database-wire-protocol fidelity is a Phase 6 spike, not a v1 guarantee.
- **Time-dependent logic.** Code that reads the wall clock will diverge
  between recording time and replay time unless the service honors a
  frozen-time mechanism Parity provides. Exchanges are flagged
  `DEPENDS_ON_TIME`; the noise model is expected to absorb the rest, but a
  low replay fidelity score is the honest signal when it can't.
- **Randomness-dependent logic.** Same idea, flagged `DEPENDS_ON_RANDOMNESS`.
- **Streaming / WebSockets / GraphQL subscriptions.** Out of scope for v1
  entirely (see product scope). A `STREAMING` flag exists so capture can at
  least mark these rather than silently mis-recording them.
- **Non-HTTP protocols.** gRPC is Phase 5; message queues and other wire
  protocols are out of scope for v1.

## Phase 1 specific limits (capture + recording)

- **Scrubbing is text-pattern based, not JSON-path based, for body content.**
  `internal/scrub` runs regex detectors (email, phone, SSN, credit card w/
  Luhn, JWT, API key shapes, IPv4, IBAN) over the serialized request/response
  body text. This catches PII regardless of where it is nested, but it
  cannot yet apply a *different* policy to two JSON paths that happen to
  contain similarly-shaped values (e.g. "redact `$.card.number` but
  preserve `$.order.id` even if it's also a long digit string"). The
  `parity.yaml` `scrub.json_paths` config is parsed by `internal/config` but
  not yet wired into `internal/scrub` — that's the Phase 2 follow-up.
- **1MB body cap.** Request and response bodies are truncated at 1MB during
  capture (flagged `LARGE_BODY_TRUNCATED` in the schema, but the capture
  proxy does not yet set that flag — follow-up).
- **Endpoint templating is heuristic.** `/users/123` and
  `/campaigns/a1b2c3d4e5f6` both template to `.../{id}`; a numeric-looking
  static path segment (e.g. a literal `/v2/` version prefix that happens to
  be all-digits in some deployment) would be mis-templated. OpenAPI-based
  exact-template import exists as an API (`Templater.RegisterExact`) but no
  loader is wired up yet.
- **Capture overhead verified only at low concurrency on developer
  hardware** (~190µs added p99 in `eval/harness`), not at the 5k RPS /
  documented-hardware budget from the product spec.
- **No Kubernetes/Docker Compose replay** — Phase 1 is capture-only, so
  nothing about replay fidelity can be claimed yet.

## Phase 2 specific limits (replay + three-way comparison)

- **Scrubbed recorded data vs. raw replay data mismatch.** Discovered via
  manual CLI testing (not hidden): a segment captured through
  `cmd/parity-capture` has PII (e.g. emails) pseudonymized by
  `internal/scrub` before it's written. When `parity replay` replays those
  requests against a live base build, the base build returns its *real*
  (unscrubbed) data. Comparing the scrubbed recorded response against the
  raw replay response then shows a spurious diff on every PII-bearing
  field, tanking the fidelity score for reasons that have nothing to do
  with the candidate build. `eval/harness/mutation_test.go` sidesteps this
  by recording its baseline directly (unscrubbed) rather than through the
  capture pipeline, which is why it reports fidelity=1.00 while a real
  `parity replay` run against a capture-produced segment would not. The
  real fix (not done this phase): either scrub the live replay response
  with the same deterministic pseudonymization before diffing, or restore
  the base/candidate database from an already-pseudonymized snapshot so
  both sides are consistently pseudonymized. Tracked as Phase 2.5 follow-up.
- **No Docker/Kubernetes `Environment`.** Only `ProcessEnvironment`
  (local subprocess) exists; see docs/plans/phase-2.md and ADR 0002.
- **No outbound dependency stubbing wired in.** `internal/stub` does not
  exist yet this phase (the demo service has no downstream calls); it's
  needed before Phase 3's N+1 detection, which does need a downstream call.
- **No database snapshot restore / auth credential injection.** The demo
  service is stateless and unauthenticated, so three-way comparison never
  needed these; a real customer service almost always will.
- **Noise model is per-run, not persisted across runs.** `internal/diff.Learn`
  builds a fresh model from each run's own A/B pairs; the spec's "noise
  model improves across PRs" (stored as a versioned artifact per endpoint)
  is not implemented.
- **Mutation catalog has 6 entries**, not the ~15+ in the full spec. See
  docs/plans/phase-2.md for the honest reasoning.

## Resolved

(none yet — Phase 1 is the first phase)
