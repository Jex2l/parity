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

## Known gaps

- `action.yml` has never run inside a real GitHub Actions runner — there
  is no GitHub Actions environment available in this dev environment to
  execute it against, matching the same limitation `docs/limits.md`
  already states for the GitHub App integration generally ("no real
  GitHub App has ever been registered/installed"). It was reviewed
  carefully against the Actions composite-action schema and against how
  `cmd/parity` and `cmd/parity-ci`'s flags actually parse, but "reviewed
  carefully" is not "verified by running it."
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
