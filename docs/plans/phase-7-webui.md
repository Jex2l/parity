# Phase 7 (unplanned) — interactive web report viewer

Not part of the original 6-phase plan. The user asked to "build the UI...
a very detailed one with interactive surfaces" for the product — the
spec's own section 6.9 calls for exactly this ("minimal web report
page... full cluster explorer, example browser with before/after
side-by-side JSON diffs, perf charts, and noise-model inspector") but it
was never built across phases 1–6. This is that piece.

## What was built

- `cmd/parity-webui` — a single Go binary serving an embedded (`go:embed`)
  static frontend plus a small JSON API (`/api/report`, `/api/runs`) that
  always re-reads `report.json` from disk, so a new `parity replay` run
  shows up on refresh with no server restart.
- `cmd/parity-webui/static/{index.html,app.css,app.js}` — a vanilla
  JS/CSS single-page app (no framework, no CDN dependency — this has to
  run fully offline on a local machine, same constraint as the rest of
  the repo). Interactive surfaces:
  - **Cluster explorer**: searchable/filterable (by severity, by text)
    sortable (severity / blast radius / endpoint) cluster list, with a
    toggle between signal clusters and absorbed-as-noise clusters.
  - **Example browser**: per-cluster example tabs, each showing a real
    before/after JSON comparison with inline highlighting of exactly the
    fields the diff engine flagged, plus a change table (path, type,
    before, after) and copy-to-clipboard buttons.
  - **Performance tab**: per-endpoint base/candidate p95, effect size,
    p-value, and a bar chart (pure CSS, no charting library).
  - **Noise-model inspector**: every learned (endpoint, path) →
    volatility classification, filterable by class and searchable.
  - Run switcher (sibling `*.json` files in the report's directory),
    auto-refresh polling, light/dark/auto theme (persisted to
    localStorage), keyboard navigation (arrow keys) in the cluster list,
    and hash-based deep links (`#clusters/signal/<index>/<example>`).

## Schema changes this required

The existing `report.Report`/`ClusterView` had no example bodies, no
per-change before/after values, and no noise-model listing — enough for
the CLI/MCP summary, not enough for a real inspector UI. Extended:

- `diff.ExchangeDiff` gained `Method`, `Path`, `BeforeBody`, `AfterBody`.
- `diff.Cluster` gained `Examples []Example` (full before/after bodies +
  per-change detail, not just exchange IDs).
- `diff.BuildAbsorbedClusters` — same clustering logic, applied to
  `Absorbed` changes instead of `Signal`, for the noise-model inspector's
  "what got filtered out, and why" view.
- `report.Report` gained `AbsorbedClusters` and `NoiseModel`;
  `report.ClusterView` gained `Examples`.
- `orchestrate.Run` now populates all of the above.

All backward compatible (new fields, `omitempty` where it matters);
every existing test still passed unchanged.

## Found and fixed while actually using the feature, not before

Building a UI and then *using* it — not just unit-testing the pieces in
isolation — surfaced two real, previously-undiscovered bugs in
`internal/scrub`'s phone-number detector, both found by looking at the
rendered page and noticing something looked wrong:

1. The detector's regex matched ISO-8601 date prefixes ("2024-12-31") as
   phone numbers, corrupting every timestamp in scrubbed output.
2. The same regex matched bare long digit runs with no phone-like
   separator at all — e.g. a float's decimal tail
   ("5043.6789012345") — corrupting ordinary numeric data.

Fixed by requiring a phone-like separator (`-`, `(`, `)`, space, or a
leading `+`) and excluding date-shaped prefixes, with regression tests
for both the bug and for "still catches a real phone number." See
`docs/limits.md`'s robustness-pass section and
`internal/scrub/scrub_test.go`.

## What's still a known gap

- No Go tests exercise the frontend JS itself (no headless browser test
  harness wired into `make test`) — it was verified by actually loading
  it in a real browser and interacting with every control during this
  session, not by an automated UI test suite. A regression in `app.js`
  would not be caught by `make test`.
- No auth on the server — spec 6.9 says "shareable behind auth"; this is
  explicitly a local/trusted-network tool today, not something to expose
  publicly without putting a reverse proxy with auth in front of it.
- The JSON-path highlighting in the example browser is a best-effort
  re-derivation of `internal/diff`'s path convention (including its
  keyed-array heuristic) done independently in JavaScript; if the two
  ever drift apart, a changed field just won't highlight inline — the
  change table below it is always the authoritative source, so nothing
  is silently lost, only the visual callout.
