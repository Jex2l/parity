# Parity

Behavioral verification for AI-written code via production traffic replay.

Parity captures sanitized production traffic and, on every pull request,
replays it against the PR's build in an isolated environment, then reports
what actually changed — not what unit tests happened to check.

**Status: all 6 phases of the original build plan have a working,
tested slice.** See `docs/plans/` for each phase's plan + honest summary
of what was verified vs. deferred, and `docs/decisions/` for the ADRs
behind every non-obvious choice. **Read `docs/limits.md` before trusting
any of this.** In particular: no real GitHub App has ever been
registered/installed (Phase 4), no Kubernetes cluster was available to
test the Kubernetes environment driver or `helm install` the chart
(Phase 5), and the LLM classifier was never built (no model endpoint in
this environment — only the heuristic baseline exists). Do not treat
this repo as production-ready; treat it as a working, end-to-end-tested
foundation with explicitly named gaps.

Capture → recording (Phase 1), three-way replay + diffing (Phase 2),
performance regression detection + test generation (Phase 3), the
heuristic classifier + GitHub client + MCP server (Phase 4), gRPC
diffing + privacy gating + retention (Phase 5), and a database
record/replay fidelity spike against a real Postgres (Phase 6) are all
implemented, unit tested, and — where the dependency was actually
available in this environment — exercised end to end against a real
upstream service, a real gRPC server, or a real database. Where a
dependency wasn't available (a live GitHub App, a Kubernetes cluster),
that is stated plainly in `docs/limits.md` rather than glossed over.

## 5-minute quickstart (Phase 1: capture only)

```bash
make test   # unit + property-based tests for recording, scrub, capture, config
make demo   # builds the demo service, runs it behind the capture proxy,
            # sends 2000 synthetic requests through it, and inspects the
            # resulting recording segment
```

`make demo` prints a summary like:

```json
{
  "segment": "./segments/seg-000001.zst",
  "record_count": 1899,
  "by_endpoint": { "/users": 704, "/users/{id}": 388, "...": "..." },
  "by_status": { "200": 1812, "404": 87 }
}
```

To look at individual captured exchanges (already scrubbed of PII/secrets):

```bash
./bin/parity inspect ./segments/seg-000001.zst --jsonl
cat ./segments/seg-000001.zst.jsonl | head -1 | python3 -m json.tool
```

## Architecture (Phase 1 slice)

```
client → [parity-capture reverse proxy] → upstream service
              │
              │ (off the hot path, fail-open, bounded queue)
              ▼
         scrub.Scrubber  →  recording.Writer  →  segment-*.zst + .idx
```

See `docs/architecture.md` for the full two-plane design (capture, replay,
diff, publish / control plane) once later phases land.

## Honest limitations (today)

- No replay, diffing, performance analysis, classifier, test generation, or
  GitHub integration yet — only capture and recording.
- The capture overhead test in `eval/harness` runs on developer hardware at
  low concurrency; the production budget (p99 < 1ms added at 5k RPS on
  documented hardware) is **not yet verified** at that scale.
- Docker was not available in the environment Phase 1 was built in, so
  Docker Compose/Kubernetes replay environments (Phase 2+) are unverified
  until run somewhere with a Docker daemon. See `docs/decisions/0002-*.md`.
- Scrubbing in v1 regex-matches over serialized body text rather than
  walking JSON paths; see `internal/scrub/scrub.go` doc comment and
  `docs/limits.md`.

See `docs/limits.md` for the full list, including the inherent limits of
behavioral replay (stateful flows, time-dependent logic, streaming).
