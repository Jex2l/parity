GO ?= go
SEGMENTS_DIR ?= ./segments

.PHONY: build test vet proto demo clean

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

test: vet
	$(GO) test ./...

# Regenerate internal/recording/pb/exchange.pb.go from the .proto schema.
# Requires `protoc` and `protoc-gen-go` on PATH (see docs/decisions/0001).
proto:
	protoc --go_out=paths=source_relative:internal/recording/pb -I internal/recording internal/recording/exchange.proto

# make demo: builds the demo service + capture proxy + traffic generator,
# runs traffic through capture, and inspects the resulting segment.
# This is the Phase 1 acceptance walkthrough from docs/plans/phase-1.md.
demo: build
	@rm -rf $(SEGMENTS_DIR) demo.pid capture.pid
	@mkdir -p $(SEGMENTS_DIR)
	$(GO) build -o bin/demo-service ./eval/demo-service
	$(GO) build -o bin/parity-capture ./cmd/parity-capture
	$(GO) build -o bin/parity ./cmd/parity
	$(GO) build -o bin/traffic-gen ./eval/traffic-gen
	DEMO_ADDR=:3000 ./bin/demo-service & echo $$! > demo.pid
	sleep 1
	./bin/parity-capture -config parity.demo.yaml -segments $(SEGMENTS_DIR) & echo $$! > capture.pid
	sleep 1
	./bin/traffic-gen -target http://localhost:8080 -n 2000 -concurrency 16
	sleep 1
	kill `cat capture.pid` 2>/dev/null || true
	kill `cat demo.pid` 2>/dev/null || true
	rm -f demo.pid capture.pid
	@echo "--- segment summary ---"
	./bin/parity inspect $(SEGMENTS_DIR)/seg-000001.zst

# make webui REPORT=report.json: builds and runs the interactive HTML
# report viewer against an already-written report.json (see `parity
# replay --out`). Open http://localhost:4400 once it's running.
webui: build
	$(GO) build -o bin/parity-webui ./cmd/parity-webui
	./bin/parity-webui -report $(or $(REPORT),report.json) -addr :4400

clean:
	rm -rf bin $(SEGMENTS_DIR) demo.pid capture.pid
