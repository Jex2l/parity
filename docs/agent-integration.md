# Agent integration

Coding agents are first-class users of Parity (spec 6.10). This document
describes the loop and the tools available today; it reflects what is
actually implemented (end of Phase 4), not the full future surface.

## The loop

```
agent writes a change
  → agent runs `parity replay` (or calls parity_run_replay over MCP)
      against a sampled recording, base build, candidate build
  → agent reads the returned clusters (severity-sorted, unintended-leaning
    first per the heuristic classifier)
  → agent fixes the diffs that look like accidents
  → agent reruns; a clean report (exit code 0) means proceed
  → agent opens the PR
```

Deterministic CLI exit codes (also what `parity_run_replay` summarizes):

| Code | Meaning |
|---|---|
| 0 | Clean — no signal clusters, fidelity acceptable |
| 10 | Unintended-looking diffs (or failed regression tests) found |
| 20 | Replay fidelity too low to trust the diffs — see the `reason` field |
| 30 | Infrastructure error (bad config, segment unreadable, build wouldn't start) |

## CLI

```bash
parity inspect segments/seg-000001.zst
parity replay --segment segments/seg-000001.zst \
  --base-cmd ./bin/service --candidate-cmd ./bin/service \
  --candidate-env PARITY_MUTATION=my_change --sample 2000 --out report.json
parity report report.json
parity test generate --segment segments/seg-000001.zst --out cases.json
parity test run --cases cases.json --cmd ./bin/service
```

## MCP server

`cmd/parity-mcp` exposes (tested end to end in
`cmd/parity-mcp/main_test.go` via an in-process MCP client, not mocked):

- `parity_run_replay(segment, base_cmd, candidate_cmd, candidate_env?, sample?)`
  → compact summary: total replayed, fidelity, clusters, perf regressions.
- `parity_get_report(path)` → a previously written `report.json`.
- `parity_explain_cluster(report_path, cluster_index)` → full detail for
  one cluster.
- `parity_run_regression_tests(cases_path, cmd)` → pass/fail counts and
  failure reasons for a generated test suite.
- `parity_ack_cluster(store_path, cluster_key, verdict, reason?)` →
  records reviewer feedback (`acknowledged_intended` or `bug`).

Run it with:

```bash
go build -o bin/parity-mcp ./cmd/parity-mcp
```

and point your MCP client's config at the resulting binary over stdio.
**Caveat (stated plainly, per docs/plans/phase-4.md):** this has been
tested against a real MCP client/server session using the Go SDK's
in-memory transport, which exercises the real protocol — but it has not
yet been run against an external MCP client (e.g. Claude Desktop/Code).

## Example `CLAUDE.md`/`AGENTS.md` snippet

Paste something like this into your repo so an agent working on a PR
knows to close the loop itself before handing back:

```markdown
## Before opening a PR

This repo uses Parity for behavioral verification. After making a change:

1. Build your change and the unmodified `main` branch as two binaries.
2. Run `parity replay --segment .parity/latest.zst --base-cmd ./bin/main-build
   --candidate-cmd ./bin/your-build --sample 2000 --out /tmp/report.json`.
3. Read `/tmp/report.json`. If it's clean (exit code 0), proceed.
4. If clusters are reported, read each one's `rationale`. Fix anything
   that looks unintended. Re-run step 2.
5. If fidelity is low (exit code 20), say so in the PR description rather
   than ignoring it — it means the replay itself isn't trustworthy yet.
```

## Known gaps (see docs/limits.md for full detail)

- No real GitHub App has ever been installed/authenticated against in
  this environment — the GitHub client (`internal/gh`) is unit tested
  against a fake API only.
- The classifier is the heuristic baseline only; no LLM classifier is
  wired up (no model endpoint configured here).
- `parity_run_replay`'s fidelity score will look artificially low against
  a real capture-produced (PII-scrubbed) segment — see the Phase 2
  limitation in docs/limits.md. It is accurate for segments recorded
  without the scrub step (e.g. the eval harness's own fixtures).
