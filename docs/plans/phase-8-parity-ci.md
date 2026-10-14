# Phase 8 (unplanned) — `parity-ci`: Parity as a PR gate

Not part of the original 6-phase plan or phase 7's web UI. This phase
turns the existing replay engine into something a GitHub PR workflow can
actually drop in and use, instead of every adopting repo having to write
its own glue code to turn a `report.json` into a check run and a comment.
It's the first concrete piece of a larger idea sketched in
[`parity-labs-architecture.md`](../parity-labs-architecture.md) — a
platform for verifying AI-agent actions before they ship, of which
replay-based verification (this repo) is pillar one.

## What was built

- **`cmd/parity-ci`** — a new binary, pure composition over existing,
  already-tested packages (`internal/report`, `internal/classify`,
  `internal/gh`). It does not reimplement anything: it reads a
  `report.json`, classifies each cluster with the heuristic baseline,
  renders the existing `report.RenderMarkdown` output, and posts it as
  both a check run (`gh.CreateCheckRun`) and an upserted PR comment
  (`gh.UpsertComment`) — both of which already existed from phase 4 but
  were never wired to anything that calls them outside of tests.
  - `--dry-run` renders the markdown to stdout and exits with the same
    code it would otherwise return, without contacting GitHub at all —
    useful for local testing, or a workflow step that only wants the
    comment body for something else.
  - `--api-base-url` overrides `api.github.com`, which exists purely so
    this binary is testable against a fake server (see
    `cmd/parity-ci/main_test.go`) and, incidentally, so it also works
    against a GitHub Enterprise Server instance without any code change.
  - Exit codes (0 clean / 10 unintended / 20 low-confidence / 30 infra
    error) deliberately mirror `parity replay`'s own contract from phase
    2, so a workflow gates on the same semantics whichever command
    produced the exit code.
- **`action.yml`** — a composite GitHub Action at the repo root: builds
  `parity` and `parity-ci`, runs the replay, posts the result. A repo
  that already has a capture segment and knows how to start its base and
  candidate builds can use this with `uses: <owner>/parity@<ref>` and a
  handful of inputs, instead of hand-writing the three-step `replay` →
  `report.json` → `post-to-GitHub` pipeline themselves.
- **9 new tests** in `cmd/parity-ci/main_test.go`, including one that
  actually posts to a fake `httptest` GitHub server and inspects the
  real request bodies (check run `HeadSHA`/`Conclusion`, comment body
  content) — not just that `run()` returns the right exit code.

## What this is not

This is still a single-repo CI check, not a hosted platform. There is no
dashboard across repos, no org-wide policy, no SaaS billing layer — the
broader "Parity Labs" pitch in `parity-labs-architecture.md` describes
two more pillars (grounded-reasoning verification, approval-gated
execution) that don't exist as code anywhere yet. What's real today is:
one repo can add `action.yml` to its workflow and get behavioral-diff
PR comments. That's the actual, working wedge — everything past it is
roadmap, and the architecture doc says so explicitly rather than
implying otherwise.

## Update: validated on a real GitHub Actions run (jex2l/parity PR #1)

The "never run on a real runner" gap below is now closed. A one-off
self-test branch (`test/parity-ci-selftest`, now deleted) added a
captured segment fixture and a path-scoped workflow, used this repo's
own `eval/demo-service` as both base and candidate build, and
deliberately added a `currency` field to `/stats/revenue` so the replay
had a real behavioral diff to find — not a trivially clean report.

It took 4 runs to go green, and each failure was a real bug, not a
flake:

1. **`fatal: couldn't find remote ref main`** — both this file's own
   self-test workflow and `examples/github-actions/parity-ci.yml`
   hardcoded `origin/main` for the base-branch worktree checkout. This
   repo's own default branch is `master`. Fixed by deriving the base
   ref from `github.event.pull_request.base.ref` instead of assuming a
   name — the same mistake any adopting repo without a branch named
   `main` would have hit immediately.
2. **`tar: ... Cannot open: File exists`** — `action.yml` ran its own
   `actions/setup-go@v5` (with caching) inside the composite action,
   which collided with the consuming workflow's already-populated Go
   module cache mid-extraction. Fixed by removing the action's internal
   setup-go step entirely: a consumer already needs Go on PATH to build
   its own base/candidate commands before calling this action, so
   setting it up a second time was both redundant and actively harmful.
3. **A correctly-posted result still failed the step** — the posting
   step ran under bash's default `set -e`, so `parity-ci`'s non-zero
   but meaningful exit codes (10/20/30 are outcomes, not shell errors)
   killed the script on that line, before `code=$?` could capture it.
   The check run and PR comment were posted correctly either way — only
   the workflow step's own pass/fail status was wrong. First fix
   attempt (`|| true`) was itself wrong: it stops the crash but always
   sets `$?` to 0, silently discarding the real outcome. Correct fix:
   `set +e` around the call, `code=$?`, `set -e` after.

The 4th run went fully green: `gh pr checks` showed `selftest: pass`
and the `Parity / behavioral diff` check completed with
`conclusion: neutral` (verified via `gh api .../check-runs`, not just
the CLI's summary view) — correct, since the demo's `generated_at`
timestamp field makes base-vs-base replay fidelity naturally land
around 44% on this particular fixture. The posted PR comment correctly
identified the one real cluster: `/stats/revenue`'s `$.currency` field
addition, clustered, classified `LIKELY_INTENDED` at 55% confidence
with an accurate rationale ("only new fields were added... consistent
with an intentional additive change").

All three fixes are committed on `master`; the self-test branch, its
segment fixture, and its scoped workflow file were deleted afterward —
they were scaffolding for this one validation run, not something meant
to stay in the repo permanently.

## Update: installed on a real, separate repo (v0.1.0 → v0.1.1)

The self-test above validated the Action *within* `jex2l/parity` —
useful, but it meant `github.action_path` (parity's own checked-out
code) and the working directory (also parity's own module) were always
the same Go module. That quietly masked a real bug that any actual
adopting repo would hit on the very first run.

`v0.1.0` was tagged and tested by installing it on a genuinely separate
throwaway repo
([jex2l/parity-client-test](https://github.com/Jex2l/parity-client-test),
a minimal unrelated Go service under module `example.com/widget-api`)
via `uses: jex2l/parity@v0.1.0`, with its own real captured traffic
segment (500 real requests) and its own PR with a real field addition.

**What that run found:** `action.yml`'s build step ran
`go build -o ... "${{ github.action_path }}/cmd/parity"` from the
*consumer's* working directory. Go resolves package path arguments
against the *current* module, so building a path that belongs to a
different module (`github.com/jex2l/parity`, checked out under
`_actions/`) from inside `example.com/widget-api`'s working directory
fails outright: `directory ... outside main module or its selected
dependencies`. Every self-test run passed only because parity's own
code and the working directory happened to be the same module the
entire time — the bug was invisible until a real stranger's repo tried
it. Fixed with `go build -C "${{ github.action_path }}" ...`, which
points Go at the right module before resolving the package path; a
local reproduction confirmed the exact failure and the exact fix before
it was pushed. Re-running the Action on the same test repo went green:
it correctly flagged the deliberately added `sku` field and posted the
check run + PR comment, cross-repo, for real.

Two smaller, repo-setup-only issues surfaced along the way (not
`parity` bugs, but worth naming since they're exactly what a real
client setting this up for the first time would also hit): a
`.gitignore` pattern meant for a build output binary
(`widget-api`) accidentally also matched the `cmd/widget-api/`
source directory because it wasn't anchored to the repo root, and
`parity.yaml` needs a top-level `service:` field that's easy to miss
copying from the demo config (`parity.demo.yaml` has it; a bare-minimum
example didn't).

Tagged as `v0.1.0` (the pre-fix version — never promoted as
install-ready) and `v0.1.1` (the version that's actually been installed
successfully on a separate repo).

## Update: v0.1.1's check failed on every single PR (v0.1.2)

`v0.1.1` correctly built, replayed, detected diffs, classified them,
and posted — confirmed above. What it got wrong: `outcome()` decided
the GitHub check's pass/fail purely from `len(rep.Clusters) > 0`,
completely ignoring the classification it had just computed for the PR
comment. Retesting `v0.1.1` on two more real PRs against
`jex2l/parity-client-test` — one adding a field (classified
`LIKELY_INTENDED` at 55%), one changing a value (classified `UNCERTAIN`
at 30%) — made this obvious: **both failed the check.** Every PR that
touches the service at all would fail, confidently-intentional changes
included. A check that's always red is equivalent to no check at all —
worse, actually, since it trains reviewers to stop reading it.

Fixed in `v0.1.2`: `outcome()` now takes the cluster classifications
and only returns `ConclusionFailure`/`exitUnintended` when at least one
cluster is `UNCERTAIN` or `LIKELY_UNINTENDED`. A report where every
cluster is `LIKELY_INTENDED` gets a new exit code (`exitLikelyIntended`
= 15) mapped to `ConclusionNeutral` — visible and informative, not
blocking. `action.yml`'s `case` statement needed a matching branch for
15 too — without it, that exit code fell into the `*)` default and the
step aborted as an infra error, which would have reintroduced a
different "always looks broken" failure mode.

Re-validated on two fresh PRs against the same test repo, one for each
outcome: the additive/`LIKELY_INTENDED` case now shows a **green
check** (`conclusion: neutral`, confirmed via the GitHub API, not just
the UI), and the value-change/`UNCERTAIN` case still correctly shows
**red** (`conclusion: failure`) — proving the fix doesn't just make
everything pass, it makes the right things pass.

**If you're setting this up for a client, use `v0.1.2` or later** —
`v0.1.1` technically works but will make the check look permanently
broken; `v0.1.0` fails outright on install.

## Known gaps

- The action always builds `parity`/`parity-ci` from source on every run
  (`go build` from `github.action_path`) rather than using a pinned,
  pre-built release binary — fine for a single repo's own CI, adds
  ~10-20s of Go toolchain setup per run which a published release would
  avoid.
- `--changed-files` in the action is derived with a plain `git diff
  --name-only origin/<base>...`, which assumes the checkout step fetched
  enough history to resolve that ref; a shallow checkout (the GitHub
  Actions default) will make this silently return nothing, which only
  degrades the heuristic classifier's accuracy (it still clusters and
  reports correctly), not correctness of the check itself.
