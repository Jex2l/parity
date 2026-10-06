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

## Phase 6 specific limits (stateful fidelity spike)

- **Application-level recording, not wire-protocol capture.**
  `internal/dbrecord` records at the `database/sql` call boundary (a
  narrow `DB` interface), not actual Postgres frontend/backend protocol
  bytes. Only code written against that interface benefits. See
  docs/plans/phase-6.md for the full reasoning.
- **No transactions, prepared-statement reuse, COPY, or LISTEN/NOTIFY.**
  Each `ExecContext`/`QueryContext` call is recorded independently;
  anything relying on connection/transaction state across calls isn't
  modeled.
- **Exact match only.** `ReplayDB` matches by (normalized SQL, exact
  argument values); no nearest-match fallback, no parameter-shape
  matching the way `internal/stub`'s outbound-call matcher does. An
  unmatched call returns `ErrNoMatch` honestly rather than guessing.
- **Not integrated into the replay/orchestrate pipeline.** This is a
  standalone package-level spike (as the spec itself scoped it) — it is
  not wired into `cmd/parity replay` or `eval/demo-service`.
- **No write-regression detection.** If a candidate build's write logic
  itself changed, replaying a read against the *base* build's recorded
  write result would never show that — recorded, not solved, per the
  spec's own framing of this limitation.
- **Requires a local PostgreSQL 16** (installed via Homebrew, ADR 0004)
  to exercise beyond compile-time; tests skip gracefully without one.

## Robustness hardening pass (post-Phase-6, not a numbered phase)

A focused pass fixing real correctness/reliability gaps found by
re-reading the code with "what breaks in production" in mind, rather
than adding new features. See `docs/decisions/0005-robustness-pass.md`.
Everything below was verified by a new or updated test, not just
reasoned about:

- **Capture body-cap correctness bug, found and fixed.** The capture
  proxy's request-body handling pre-read the body through a
  `io.LimitReader` and replaced `r.Body` with only what it read — for any
  upload larger than the 1MB capture cap, this silently truncated what
  was actually forwarded to the real upstream service, not just what got
  captured. Fixed with a `cappedBuffer` type (never errors on overflow,
  so wrapping it in `io.TeeReader` can't short-circuit the real read) and
  verified by `TestProxy_ForwardsFullBodyEvenWhenCaptureIsTruncated`,
  which asserts the upstream receives the full body while only the
  capped copy is captured.
- **`LARGE_BODY_TRUNCATED` is now actually set** on exchanges whose
  request or response body exceeded the capture cap (previously tracked
  in this doc as a known gap; now verified by the same test above).
- **Capture proxy shutdown no longer drops buffered exchanges.**
  `Proxy.Run`'s stop function used to close a done channel and return
  immediately, abandoning anything still sitting in the queue. It now
  drains the queue into the sink before returning. Fail-open is about
  the hot path staying fast under backpressure, not an excuse to lose
  data on an orderly shutdown.
- **Insecure default HMAC pseudonymization secret, found and fixed.**
  `cmd/parity-capture` constructed its `scrub.Scrubber` without ever
  reading a secret from config, so every real deployment would have
  silently used `internal/scrub`'s hardcoded dev-default secret —
  meaning pseudonymized values in any captured segment were trivially
  reversible by anyone who read the source. Fixed by adding
  `scrub.hmac_secret_env` to `parity.yaml` (names an environment
  variable; the secret itself is never written to the config file) and
  wiring it through `cmd/parity-capture`, which now logs a loud warning
  if it falls back to the insecure default instead of doing so silently.
- **Scrubbed-recording-vs-raw-replay fidelity bug, fixed.** This was the
  headline Phase 2 limitation: a capture-produced segment's recorded
  response is PII-scrubbed, but a live replay response is not, so every
  PII-bearing field looked like a spurious regression. Fixed by adding
  an optional `Scrubber` to `internal/orchestrate.Config` — when set, it
  scrubs every live replay response (base-A, base-B, and candidate)
  before any comparison, so both sides are consistently pseudonymized.
  Wired into `parity replay` as `--hmac-secret-env` (must match whatever
  secret `parity-capture` used to produce the segment). Verified by
  `TestRun_ScrubberFixesPIIFalsePositive`, which reproduces the bug
  (fidelity=0.00 without the fix) and then shows it fixed (fidelity=1.0,
  zero clusters) in the same test run.
- **Truncated segments no longer hard-fail `parity replay`.**
  `orchestrate.LoadExchanges` used to treat `recording.ErrTruncated` as a
  fatal error, discarding every exchange successfully read before the
  truncation point along with it — directly contradicting
  `internal/recording`'s own stance that earlier records in a truncated
  segment remain valid. Fixed to return the partial results.
- **Subprocess zombie/hang risk in `ProcessEnvironment`, fixed.**
  `Teardown` called `Process.Kill()` but never `Wait()`ed on the
  process, leaving a zombie on Unix until the parent exited — a real
  cost for a long-running process (the MCP server) that provisions many
  short-lived replay environments over its lifetime. Also, a subprocess
  that crashed immediately (bad binary, wrong port) used to make
  `Provision` spin for the full 5s health-check timeout before reporting
  a confusing "timed out" error. Fixed with a single background
  `cmd.Wait()` goroutine (deduplicated via `sync.Once` so it's safe for
  both the Provision-failure path and Teardown to trigger cleanup) that
  both reaps the process and lets health-checking fail fast on an early
  exit. Verified by `TestProcessEnvironment_DetectsEarlyCrashQuickly`
  (209ms instead of 5s) and `TestProcessEnvironment_TeardownReapsAndIsIdempotent`
  (proves calling `Teardown` twice, or after a failed `Provision`, never
  hangs).
- All of the above re-verified under `go test -race` on the touched
  packages (`internal/capture`, `internal/replay`, `internal/orchestrate`,
  `eval/harness`) — clean, no new data races introduced.

## Interactive web report viewer (post-Phase-6, not a numbered phase)

Built `cmd/parity-webui` per spec 6.9. See `docs/plans/phase-7-webui.md`
for the full writeup. Headline items:

- **No automated frontend tests.** `app.js` was verified by actually
  loading the page in a real browser and interacting with every control
  (search, filters, sorting, example tabs, run switching, theme toggle)
  during this session — but `make test` does not exercise it, so a
  regression there would go uncaught by CI.
- **No auth.** This is a local/trusted-network tool; spec 6.9's "shareable
  behind auth" would need a reverse proxy in front of it.
- **Two real scrub bugs found and fixed while using the UI** (not found
  by a unit test in advance): the phone-number detector's regex matched
  ISO-8601 date prefixes (corrupting every timestamp in scrubbed output)
  and bare long digit runs with no phone-like separator (corrupting a
  float's decimal digits, e.g. a revenue total). Both fixed by requiring
  an actual phone-like separator or a leading `+`, with regression tests
  for the bug and for "a real phone number is still caught." This is
  exactly the kind of gap that only surfaces when a feature is actually
  exercised end-to-end rather than only unit-tested in isolation —
  worth noting as a general argument for building (and using) the UI,
  beyond its value as a deliverable in its own right.

## Resolved (phase-numbered items that were fixed later, not in the robustness pass above)

(none yet)
