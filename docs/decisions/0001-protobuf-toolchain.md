# 0001: Protobuf toolchain pinning

Context: the build environment had neither `protoc` nor `protoc-gen-go`
installed. `protoc` was available via the Anaconda distribution on PATH;
`protoc-gen-go` was installed via `go install
google.golang.org/protobuf/cmd/protoc-gen-go@latest`.

Decision: commit generated `*.pb.go` files to the repo (common Go practice)
so `make build`/`make test` do not require protoc at all for consumers —
only contributors regenerating the schema need the toolchain. Document exact
install commands in `Makefile` under a `proto` target.

Consequences: generated code must be regenerated and committed together
with `.proto` changes; CI does not regenerate, it only builds.
