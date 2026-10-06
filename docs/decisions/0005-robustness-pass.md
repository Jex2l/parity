# 0005: Post-Phase-6 robustness pass

Context: after all 6 phases had a working, tested slice, the user asked
to "make it robust." Rather than guess at what that means in the
abstract, this was treated as an instruction to re-read the existing
code specifically looking for correctness and reliability gaps — things
that would bite a real user, not new features.

Decision: fix what was actually found, each verified by a new or
updated test (not asserted), and document every fix in
`docs/limits.md`'s "Robustness hardening pass" section rather than
quietly folding it into history:

1. A real correctness bug in the capture proxy: request bodies larger
   than the capture cap were truncated on the wire to upstream, not just
   in what got captured. Fixed with a non-erroring `cappedBuffer` used
   via `io.TeeReader`.
2. The `LARGE_BODY_TRUNCATED` fidelity flag, previously tracked in
   `docs/limits.md` as unimplemented, is now actually set.
3. Capture proxy shutdown now drains buffered-but-unprocessed exchanges
   instead of dropping them.
4. A real security gap: the capture binary never read a configurable
   HMAC secret, so every deployment silently used the insecure built-in
   default. Added `scrub.hmac_secret_env` to `parity.yaml`.
5. The headline Phase 2 limitation (scrubbed recorded data vs. raw
   replay data causing spurious fidelity failures) is fixed via an
   optional `Scrubber` on `orchestrate.Config`, applied consistently to
   every live replay response before comparison.
6. `orchestrate.LoadExchanges` no longer hard-fails on a truncated
   segment; it returns the valid partial results, consistent with
   `internal/recording`'s own stance on truncation.
7. `ProcessEnvironment` no longer risks zombie processes or a 5-second
   stall on an immediately-crashing subprocess.

Consequences: no new features were added in this pass — deliberately.
"Robust" was interpreted as "the existing surface should not have known
correctness bugs or obvious reliability gaps," not "add more scope."
Everything fixed here already existed as either a silent bug (1, 4) or a
documented-but-unfixed limitation (2, 5, 6, 7 partially). All changes
were re-verified with `go test -race` on the touched packages.
