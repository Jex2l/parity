# Setup guide: installing `parity-ci` on your repo

This assumes zero prior context with this codebase. Follow it top to
bottom; every step here was just run for real, on a separate throwaway
repo, before this doc was written — not from memory. If you hit
anything not covered here, treat that as a doc bug, not a you problem.

**Use `jex2l/parity@v0.1.3` or later.** `v0.1.0` fails on every real
install (a cross-module build bug); `v0.1.1` installs and detects
diffs correctly but its GitHub check fails on *every* PR regardless of
how confidently intentional the change looks, which trains reviewers to
ignore it (fixed in `v0.1.2`); `v0.1.3` adds grounded classification —
the classifier searches the PR's actual diff text for evidence before
falling back to structural rules, citing the real diff line and scoring
higher confidence than a structural guess alone. All three fixes are
written up in full in `docs/plans/phase-8-parity-ci.md`.

> ⚠️ **If your repo is private, test this on a throwaway PR before
> relying on it.** Validating `v0.1.3` found a confirmed,
> account-level issue where `actions/checkout` fails with "repository
> not found" on every private repo tried (public repos work every
> time) — most likely a GitHub plan/billing Actions-minutes
> restriction, not a bug in this code, but unverified whether it's
> universal or specific to the account it was found on. See the phase-8
> doc's "confirmed account-level private-repo blocker" section before
> promising this works on a private repo.

## What you need before starting

- A service with an HTTP health-check endpoint (any path works, you'll
  configure it).
- Your service must read its listen port from an environment variable
  you control (e.g. `PORT` or `DEMO_ADDR` — any name, you specify it).
- Go installed in your CI workflow already (you need it anyway to build
  your own service).

## Step 1 — install `parity` and `parity-capture` locally

```bash
go install github.com/jex2l/parity/cmd/parity@v0.1.3
go install github.com/jex2l/parity/cmd/parity-capture@v0.1.3
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
check and a PR comment, automatically. **The check fails only when at
least one changed cluster is classified `UNCERTAIN` or
`LIKELY_UNINTENDED`** — a cluster the classifier confidently calls
`LIKELY_INTENDED` shows up as a visible, informative, non-blocking
(neutral) check instead of a hard failure. (Earlier `v0.1.1` failed on
every single diff regardless of classification — confirmed live on two
back-to-back test PRs before `v0.1.2` fixed it; see
`docs/plans/phase-8-parity-ci.md`.) Two real, confirmed examples from
validating that fix (on `v0.1.2`, before `v0.1.3`'s grounded
classification existed — the confidence numbers below are from the
structural heuristic; `v0.1.3` cites the actual diff line and scores
higher when it finds one, see the phase-8 doc for a live `v0.1.3`
example) — same tool, two different outcomes for two different kinds of
change:

**An additive, low-severity change (passes, neutral):**
```
## Parity / behavioral diff

Replayed 500 requests. Fidelity: 100%.

1 cluster(s) found (1500 signal change(s), 0 absorbed as noise).

[LOW] /widgets — LIKELY_INTENDED (500 occurrences)
  Changed paths: $[id=1].sku, $[id=2].sku, $[id=3].sku
  Why LIKELY_INTENDED (55% confidence): only new fields were added
  (low severity), consistent with an intentional additive change
```

**A value change with no supporting context (fails, correctly):**
```
## Parity / behavioral diff

Replayed 500 requests. Fidelity: 100%.

1 cluster(s) found (500 signal change(s), 0 absorbed as noise).

[MEDIUM] /widgets — UNCERTAIN (500 occurrences)
  Changed paths: $[id=1].price_cents
  Why UNCERTAIN (30% confidence): no strong structural signal either
  way; needs human (or LLM classifier) judgment
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
