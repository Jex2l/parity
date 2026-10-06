# Phase 6 — Stateful fidelity (design + spike)

Per the original spec, this phase is explicitly scoped as "design doc +
prototype... do not promise completeness." That framing is followed
literally here.

## Goal
Design and prototype capturing and replaying database query/result pairs
so write-heavy/stateful endpoints can replay with higher fidelity than
snapshot-restore alone, and document honestly what is and isn't
reproducible.

## Scope decision: application-level recording, not wire-protocol capture

The spec's own section 6.4 calls this "Postgres wire-protocol recording
and replay." A literal implementation intercepts bytes on the TCP
connection between the application and Postgres (parsing the Postgres
frontend/backend protocol — `Parse`/`Bind`/`Execute`/`DataRow` messages,
etc.). That is a much larger undertaking than a "spike" and has real
footguns (prepared-statement protocol state, COPY mode, SSL negotiation).

Instead, this phase records at the `database/sql` call boundary: a thin
wrapper around `*sql.DB` records each query's normalized SQL text,
arguments, and result (rows or rows-affected/last-insert-id/error) to a
JSONL file, and a replay counterpart serves recorded results matched by
(normalized SQL, arguments) without hitting a real database at all. This
captures the same essential information (what the app asked, what the
DB answered) without needing to speak the wire protocol, at the cost of
only working for apps that go through Go's `database/sql` (not raw
`libpq`/COPY/LISTEN-NOTIFY). Documented here and in docs/limits.md as a
deliberate scope reduction, not a silent one — a true wire-protocol
recorder remains future work if app-boundary recording proves
insufficient for some customer's stack.

This environment does have a real local PostgreSQL 16 (installed via
Homebrew for this phase, no Docker needed) — so the spike is tested
against an actual Postgres instance doing actual INSERT/SELECT, not
mocked.

## Files
- `internal/dbrecord/dbrecord.go` — the narrow `DB` interface (ExecContext/
  QueryContext) this spike records/replays against, and `RealDB`, a thin
  adapter over `*sql.DB` that materializes results eagerly
- `internal/dbrecord/recorder.go` — `RecordingDB` logs every call to
  JSONL before delegating to the real DB
- `internal/dbrecord/replay.go` — `ReplayDB` serves recorded results by
  (normalized SQL, args) exact match; `ErrNoMatch` (not a fabricated
  empty result) on anything unrecorded
- `internal/dbrecord/recorder_test.go`, `replay_test.go` — against a
  real local PostgreSQL 16 (installed via Homebrew for this phase);
  skip (not fail) when no Postgres is reachable, so `make test` stays
  green on a machine without it

Deviation from the original file list: rather than adding a
`DATABASE_URL`-gated endpoint to `eval/demo-service` and a separate
`eval/harness/dbrecord_test.go`, the write-dependent-read scenario and
its fidelity measurement live directly in
`internal/dbrecord/replay_test.go` (`TestFidelity_SnapshotOnlyVsRecordedReplay`).
This is a smaller surface than modifying the shared demo service, and
keeps the spike self-contained to the package it's actually spiking.

## What this spike does NOT attempt (write-side-effect handling)
The spec also asks for "write-side-effect handling strategies." This
phase's recorder captures what a write query returned (e.g. the inserted
row's generated ID) well enough to replay a read that depends on it, but
does not implement: transactional isolation between concurrent replayed
writes, replaying against a snapshot that's rolled back between runs, or
detecting when a write's recorded result has diverged from what the
*candidate* build's modified write logic would actually do (replaying a
recorded INSERT result against a candidate that changed the insert logic
would just show you the old result, not catch a regression in the write
itself — reads downstream of it will look right when they shouldn't).
This asymmetry is the main reason the spec treats stateful fidelity as
inherently limited, not a solved problem — recorded here, not solved.

## Accept
On a real Postgres table, replaying a write-dependent read against a
database truncated to simulate a snapshot taken *before* the write shows
0/1 rows found (fidelity: false); replaying the same read from the DB
query/result recording reproduces the original response exactly (1/1
rows, fidelity: true). Verified by actually running
`TestFidelity_SnapshotOnlyVsRecordedReplay` against a real local
Postgres — not asserted from first principles.

## Phase 6 summary (what actually happened)

**Built and verified by running it, against a real database:**
`internal/dbrecord`'s full record → replay round trip. PostgreSQL 16 was
installed via Homebrew specifically for this phase (no Docker needed —
matches the project's "prefer boring technology" principle). The
headline result, logged by the test itself:

```
snapshot-only fidelity for this write-dependent read: false (got 0 rows, original had 1)
DB-recording replay fidelity for this write-dependent read: true (got 1 rows, original had 1)
```

This is the concrete, measured version of the spec's thesis for this
phase: a database snapshot alone cannot faithfully replay a read that
depends on a write made after the snapshot was taken, but recording the
actual query/result pairs at the time of capture closes that gap
exactly, for requests whose dependencies were captured.

**Scope reduction, decided and documented up front (not discovered
after the fact):** recording happens at the `database/sql` call
boundary (a `DB` interface with `ExecContext`/`QueryContext`), not the
Postgres wire protocol. Only application code written against this
narrow interface benefits — not arbitrary `database/sql` usage, and
nothing using raw `libpq`, prepared-statement reuse across calls,
transactions, or `COPY`/`LISTEN`/`NOTIFY`. A JSON round-trip through the
JSONL log also collapses some Go numeric types (e.g. `int32` becomes
`float64`), handled by comparing formatted values rather than strict
type-equality — the same choice `internal/diff` already makes for JSON,
applied consistently here rather than inventing a new rule.

**Not attempted, per the spec's own scoping for this phase:**
write-side-effect handling strategies beyond "record what a write
returned" (no transactional isolation between concurrent replayed
writes, no detection of a candidate's write logic itself regressing —
see the "What this spike does NOT attempt" section above, which was
written before implementation and held up unchanged afterward). No
integration into `eval/demo-service` or the replay/orchestrate pipeline
— this remains a standalone, package-level spike, as scoped.
