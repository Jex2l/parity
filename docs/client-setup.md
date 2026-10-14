# Setup guide: installing `parity-ci` on your repo

This assumes zero prior context with this codebase. Follow it top to
bottom; every step here was just run for real, on a separate throwaway
repo, before this doc was written — not from memory. If you hit
anything not covered here, treat that as a doc bug, not a you problem.

**Use `jex2l/parity@v0.1.1` or later.** `v0.1.0` fails on every real
install — see `docs/plans/phase-8-parity-ci.md` for why.

## What you need before starting

- A service with an HTTP health-check endpoint (any path works, you'll
  configure it).
- Your service must read its listen port from an environment variable
  you control (e.g. `PORT` or `DEMO_ADDR` — any name, you specify it).
- Go installed in your CI workflow already (you need it anyway to build
  your own service).

## Step 1 — install `parity` and `parity-capture` locally

```bash
go install github.com/jex2l/parity/cmd/parity@v0.1.1
go install github.com/jex2l/parity/cmd/parity-capture@v0.1.1
```

## Step 2 — write `parity.yaml` in your repo root

```yaml
service: your-service-name   # required — a plain label, used in output only
capture:
  listen: ":8081"             # parity-capture listens here
  upstream: "http://localhost:4000"   # your real service's address
  sampling:
    default_rate: 1.0
    interesting: true
  deny_paths: ["/health"]     # don't capture your own health checks
scrub:
  default_policy: pseudonymize
  headers_allow: [content-type, accept]
```

The `service:` field is easy to miss if you're copying a minimal
example instead of a full one — `parity-capture` refuses to start
without it (`invalid parity.yaml: service: required`).

## Step 3 — capture a real traffic segment

Run your service, then run `parity-capture` in front of it, then send
it real (or realistic synthetic) traffic:

```bash
YOUR_PORT_ENV=:4000 ./your-service &
parity-capture -config parity.yaml -segments ./segments &
# now send traffic through :8081 (parity-capture's listen port), not
# directly to your service on :4000 — e.g. point a staging load test,
# or just curl it a few hundred times for a first segment
curl http://localhost:8081/your-real-endpoint
```

Stop both processes. You now have `segments/seg-000001.zst` (and a
`.zst.idx` sidecar) — commit both to your repo (they're small; a 500
request segment is roughly 30-120KB depending on response size).
`parity inspect segments/seg-000001.zst` shows you what got captured.

## Step 4 — add the workflow

Copy `examples/github-actions/parity-ci.yml` from this repo into
`.github/workflows/parity-ci.yml` in your repo, and edit exactly these
three things:

1. The build step's package path (`./cmd/widget-api` or wherever your
   `main.go` lives — the examples file uses `.` for a single-package
   repo).
2. `segment:` — point it at your committed segment path.
3. `port-env:` / `health-path:` if you didn't use `DEMO_ADDR` / `/health`.

No GitHub secret needs to be created — `${{ secrets.GITHUB_TOKEN }}` is
provided automatically by Actions, and the `permissions:` block already
in the example file (`pull-requests: write`, `checks: write`) is all it
needs.

## Step 5 — open a PR and watch it work

That's it. On the next PR, you'll see a `Parity / behavioral diff`
check and a PR comment, automatically. **The check fails whenever any
behavioral difference is found, regardless of whether it looks
intentional** — that's deliberate: the comment's classification
(`LIKELY_INTENDED` / `LIKELY_UNINTENDED` / `UNCERTAIN`) is context for a
human reviewer, not an auto-pass gate. A real, confirmed example from
validating this exact flow:

```
## Parity / behavioral diff

Replayed 500 requests. Fidelity: 100%.

1 cluster(s) found (1500 signal change(s), 0 absorbed as noise).

[LOW] /widgets — LIKELY_INTENDED (500 occurrences)
  Changed paths: $[id=1].sku, $[id=2].sku, $[id=3].sku
  Why LIKELY_INTENDED (55% confidence): only new fields were added
  (low severity), consistent with an intentional additive change
```

## Known rough edges, named honestly

- **The replay engine builds `go build` in your CI runner from source
  on every run** (~5-10s). A published release binary would be faster;
  not built yet.
- **`--changed-files` needs full git history** (`fetch-depth: 0` in the
  checkout step, already in the example) to compute correctly — a
  shallow checkout silently degrades classifier accuracy, not
  correctness.
- **No LLM-based classification yet** — today's classifier is
  structural rules, not semantic understanding. See
  `docs/limits.md` and, if relevant to you,
  `pillar2-grounded-classifier` in the separate `parity-labs` repo for
  what a grounded (diff-evidence-based) classifier looks like — not
  wired into `parity-ci` yet.
