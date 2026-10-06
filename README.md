# Parity

Behavioral verification for AI-written code via production traffic replay.

Parity captures sanitized production traffic and, on every pull request,
replays it against the PR's build in an isolated environment, then reports
what actually changed — not what unit tests happened to check.

**Status: Phase 1 of 6 complete.** See `docs/plans/` for the phased build
plan and `docs/decisions/` for the ADRs behind every non-obvious choice.
Phase 1 proves the capture → record pipeline end to end; replay, diffing,
GitHub integration, and the classifier land in later phases and are not
built yet. Do not treat this repo as production-ready.

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
