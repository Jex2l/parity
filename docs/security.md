# Security and privacy (Phase 1 slice)

Full threat model grows with each phase; this is what's true today.

## What is captured

The capture proxy (`internal/capture`, `cmd/parity-capture`) records, per
sampled HTTP exchange: method, path, query string, an allowlisted subset of
headers, request/response bodies (capped at 1MB), status code, and timing.
`Authorization`, `Cookie`, `Set-Cookie`, and `X-Api-Key` headers are always
stripped regardless of the configured allowlist (`internal/scrub/scrub.go`,
`alwaysStrippedHeaders`).

## Where it lives

Segments are written to local disk under the directory passed to
`parity-capture -segments` (default `./segments`), compressed with zstd.
Nothing leaves the process by network in Phase 1 — there is no control
plane yet, so "what leaves the customer's perimeter" is trivially "nothing"
until Phase 4 introduces the GitHub App / control plane publisher.

## Scrubbing

Before being written to a segment, every exchange passes through
`scrub.Scrubber.ScrubExchange`, which:

1. Strips non-allowlisted and always-sensitive headers.
2. Runs pattern detectors over body text: email, phone, SSN, credit card
   (with Luhn validation to cut false positives), JWT, Stripe-style API key
   shapes, IPv4, IBAN.
3. Replaces each match with a deterministic, format-preserving
   pseudonymized token derived via HMAC-SHA256 over a per-process secret
   (`scrub.Config.HMACSecret` — **must** be set to a real per-tenant secret
   in any non-dev deployment; `cmd/parity-capture` does not yet read one
   from config, which is a Phase 2 follow-up tracked in `docs/limits.md`).
4. Records a `scrub_report` on the exchange listing what was redacted and
   by which rule, for audit.

Measured recall on a 9-item synthetic PII corpus
(`internal/scrub/scrub_test.go::TestScrubRecall_PIICorpus`): **100%**. This
is a small, synthetic corpus, not a production-scale benchmark; the eval
harness target (section 7 of the product spec) is >=99% recall at scale,
which Phase 1's corpus is too small to claim with statistical confidence.

## Known gaps (tracked honestly, not hidden)

- No entropy-based fallback detector wired into the main scrub path yet
  (`scrub.LooksLikeSecret`/`ShannonEntropy` exist and are unit-tested but
  not called from `ScrubExchange`).
- No `scrub audit` CLI tool yet (spec section 6.3) — Phase 2.
- No encryption-at-rest config for segment storage yet — Phase 1 writes
  plain files to local disk; object storage + customer-managed keys are
  Phase 4+ (control plane) concerns.
- No log-scrubbing tests yet to catch secrets leaking into `slog` output.
