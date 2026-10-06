# Phase 1 — Foundations and recording

## Goal
Working capture-to-segment pipeline on a demo service: reverse-proxy capture
with fail-open behavior, Protobuf `Exchange` recording format with zstd
segments + index, endpoint templating, sampling, PII scrubbing, and a demo
service + traffic generator to exercise it. Accept when `make test` and
`make demo` pass and the overhead/chaos benchmarks in the acceptance
criteria hold.

## Files
- `go.mod`, `Makefile`, `.github/workflows/ci.yml`
- `internal/recording/exchange.proto` + generated `exchange.pb.go`
- `internal/recording/segment.go` — writer/reader, zstd, index, JSONL export
- `internal/recording/segment_test.go` — round-trip + truncation-recovery property tests
- `internal/scrub/scrub.go` — layered detectors (header allowlist, JSON-path rules,
  pattern detectors: email/phone/CC-Luhn/SSN/JWT/API-key/IP, entropy fallback),
  format-preserving HMAC pseudonymization
- `internal/scrub/scrub_test.go`
- `internal/capture/proxy.go` — reverse proxy, ring buffer, fail-open, sampling
  (uniform/per-endpoint/reservoir/"interesting"), endpoint templating
- `internal/capture/proxy_test.go` + chaos test (recorder killed mid-traffic)
- `internal/config/config.go` — `parity.yaml` loader + JSON-schema validation
- `cmd/parity-capture/main.go` — capture sidecar binary
- `cmd/parity/main.go` — `parity inspect <segment>` CLI
- `eval/demo-service/` — Go HTTP JSON API, ~6 endpoints for Phase 1 (grows later)
- `eval/traffic-gen/` — synthetic load generator
- `eval/harness/overhead_test.go` — p99 latency / CPU overhead benchmark

## Interfaces (v1)
- `recording.Writer.Write(*pb.Exchange) error`, `recording.Reader.Next() (*pb.Exchange, error)`
- `capture.Sampler.ShouldSample(endpoint string, meta SampleMeta) (bool, weight float64)`
- `scrub.Scrubber.ScrubExchange(*pb.Exchange) (*pb.Exchange, ScrubReport)`

## Risks / deviations
- Protobuf toolchain (`protoc` + `protoc-gen-go`) had to be installed in this
  environment; documented so CI pins versions. See ADR 0001.
- No Kubernetes/Docker Compose replay yet (Phase 2) — Phase 1 only proves
  capture + recording.
- Docker daemon is not running in this environment; integration tests that
  need containers are deferred/skipped with a clear skip message rather than
  faked. See ADR 0002.
- Go module path chosen as `github.com/jex2l/parity` (no remote yet) — ADR 0003.
