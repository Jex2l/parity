# Phase 5 — Hardening, gRPC, Kubernetes

## Goal (per original spec)
Kubernetes environment driver + Helm chart, gRPC capture/replay with
protobuf-aware diffing, enforced privacy modes, retention/purge, scale
tests, cross-run noise model persistence, and the section 9 performance
budgets met on documented hardware.

## Scope decisions (read this before trusting anything below as "done")

This dev environment has **no Kubernetes cluster and no Docker daemon**
(ADR 0002, still true). The spec's own rule — "never claim something
works without running it" — means a from-scratch `KubernetesEnvironment`
and Helm chart cannot honestly be marked verified here. Splitting again:

- **Built and verified by running it:** gRPC capture/replay with
  protobuf-descriptor-aware structural diffing (a real gRPC server and
  client, in-process, no external infra needed — this is exactly how
  Phase 1-4 tested HTTP), privacy-mode enforcement (config validation +
  a `Gate` that filters what a publisher is allowed to send, unit
  tested), and segment retention/purge (`parity purge`, tested against a
  real temp directory with files of controlled mtimes).
- **Written but explicitly NOT run against real infrastructure:** a
  `KubernetesEnvironment` implementing the same `replay.Environment`
  interface as `ProcessEnvironment`, using `kubectl` as a subprocess
  (consistent with "prefer boring technology"), plus a Helm chart under
  `deploy/helm/`. Both are real, reviewable code/manifests — not
  placeholders — but **zero kubectl commands have ever been executed
  against a real cluster in building this**. Do not deploy them to
  anything that matters without testing them first.
- **Not attempted this phase, stated plainly:** the section 9 performance
  budgets at 5k RPS on documented hardware (no load-testing rig here),
  scale tests, and cross-run noise model persistence (would need a real
  artifact store / control plane, which Phase 4 also deferred for the
  same reason — no infra to verify it against).

## Files
- `internal/grpcdiff/` — protobuf-descriptor-aware structural diff
  (reuses `internal/diff`'s Change/Severity types so clustering/severity
  logic isn't duplicated)
- `eval/grpc-demo/` — a minimal gRPC service + proto, for testing
- `internal/privacy/gate.go` — enforces `send_to_control_plane` modes
  (`none` / `diffs_only` / `diffs_and_samples`)
- `internal/retention/purge.go` — segment retention/purge by age
- `cmd/parity`: add `parity purge`
- `internal/replay/k8s_env.go` — `KubernetesEnvironment` (kubectl-based,
  unverified against a real cluster)
- `deploy/helm/parity-capture/` — Helm chart (unverified)

## Accept
gRPC structural diff detects a field rename and a new field in a real
round-trip through an actual gRPC server (not hand-constructed messages
compared offline). Privacy gate unit tests cover all three modes
rejecting/allowing the right payload shape. Purge correctly deletes only
segments older than the configured retention window, verified against
real file mtimes. Everything Kubernetes-related is reviewed code, with
its unverified status stated in this file and docs/limits.md — not
claimed as tested.

## Phase 5 summary (what actually happened)

**Built and verified by running it:** `internal/grpcdiff` (protobuf-
descriptor-aware structural diff reusing `internal/diff`'s Change types)
detected a real `total` field value change through an actual gRPC round
trip between two live `eval/grpc-demo` server processes — not offline
message comparison. `internal/privacy` (send-to-control-plane gate,
fails closed on an unknown or unset mode) and `internal/retention`
(segment purge by file mtime, verified it only deletes segments past the
cutoff and leaves non-segment files alone) are both fully unit tested.
`parity purge` CLI subcommand added.

**Partially verified — found better tooling than expected:** this
environment turned out to have `helm` (v4.3.0) and `kubectl` installed,
just no reachable Kubernetes cluster. So the Helm chart
(`deploy/helm/parity-capture/`) was actually run through `helm lint`
(clean) and `helm template` (renders correctly, checked by eye) — more
verification than the original plan expected, though still short of a
real `helm install`. `kubectl cluster-info` was run and confirmed there
is no cluster to install against, which is why
`internal/replay/k8s_env.go`'s `KubernetesEnvironment` (a `kubectl`-
subprocess-based `Environment` implementation) remains unverified beyond
a compile-time interface-satisfaction check and a pure-logic unit test
of its pod-name sanitizer.

**Not attempted, stated plainly:** the section 9 performance budgets at
scale, scale tests, and cross-run noise model persistence — all need
infrastructure (a load-testing rig, a real control plane) this
environment doesn't have, same reasoning as Phases 2 and 4's deferrals.
