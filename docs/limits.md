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

## Phase 3 specific limits (performance analysis + test generation)

- **No resource metrics (CPU/memory/DB-query counts).** The demo service
  has no outbound dependencies or cgroup instrumentation wired up, so
  there is nothing real to measure yet; building this without something
  to verify against would be unverifiable. N+1 detection depends on this
  and is deferred with it.
- **Mann-Whitney U uses a normal approximation**, not an exact permutation
  test. Fine at the sample sizes this product targets (always well above
  the ~20-sample threshold where the approximation is reliable), but
  worth knowing if someone reduces `--sample` to something tiny.
- **Test generation emits only the portable YAML/JSON format**; the
  spec's optional language-native generators (Go httptest, pytest,
  vitest) don't exist yet.
- **Masking only handles UUID- and timestamp-shaped strings**, not the
  full noise-class taxonomy from `internal/diff` (no `<ANY_FLOAT_WITHIN>`
  matcher yet, for example) — a generated test could still flake on a
  field the diff engine's noise model would correctly absorb but testgen
  doesn't know to mask the same way. Tracked as a follow-up to unify the
  two.

## Phase 4 specific limits (GitHub integration, classifier, agent integration)

- **No live GitHub App.** Not registered, not installed on any repo, no
  webhook ever received, no real check run or PR comment ever posted.
  `internal/gh` is correct against a fake GitHub API (`httptest`) and
  that's as far as it's been verified.
- **No LLM classifier.** Only the required-first heuristic baseline
  (`internal/classify`) exists. No model endpoint is configured in this
  environment to build/test one against.
- **No control-plane run state machine / Postgres.** The spec's
  `queued → provisioning → ... → done|failed` persisted state machine
  (6.9) and `cmd/parity-controlplane` don't exist yet.
- **MCP server untested against an external client.** Verified via the
  Go SDK's in-memory transport with a real client/server session (genuine
  protocol compliance), never against Claude Desktop/Code or another
  separate MCP client process over stdio.
- **Classifier confidence numbers are not calibrated** against a labeled
  eval set (spec 6.7.3) — they're hand-picked per rule, documented as such
  in code comments, not presented as a measured probability.

## Phase 5 specific limits (hardening, gRPC, Kubernetes)

- **`internal/grpcdiff` only diffs scalar and nested-message fields.**
  Repeated and map fields are rendered as "`<list len=N>`"/"`<map
  len=N>`" and compared only by length — a reordered or
  element-wise-changed list won't get the keyed-array treatment
  `internal/diff` gives JSON arrays. Follow-up: port that logic.
- **Proto3 scalar field presence is inherently ambiguous.** A scalar
  field "cleared to its zero value" and "never set" look identical over
  the wire in plain proto3 (no `optional` keyword). `grpcdiff` always
  compares values directly for scalars, which means it reports a
  VALUE_CHANGED when a field goes from non-zero to zero, but can't
  distinguish "explicitly set to zero" from "removed" the way JSON's
  `FIELD_REMOVED` can. This is a protobuf limitation, not a code bug.
- **`KubernetesEnvironment` has never run against a real cluster.**
  `kubectl` is installed in this dev environment but there's no cluster
  (`kubectl cluster-info` fails to connect). Only compile-time interface
  conformance and a pure pod-name-sanitizer unit test exist. Do not use
  in production without testing against a real cluster first.
- **The Helm chart was lint/template-verified, not install-verified.**
  `helm lint` and `helm template` both pass, but nothing was ever
  `helm install`ed anywhere.
- **No cross-run noise model persistence, no scale tests, no load-tested
  performance budgets.** All three need infrastructure this environment
  lacks (an artifact store/control plane, a load-testing rig).

## Resolved

(none yet — Phase 1 is the first phase)
