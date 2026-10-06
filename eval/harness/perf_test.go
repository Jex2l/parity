// perf_test.go is the Phase 3 scoring harness for latency regression
// detection: it runs the real three-way comparison against a genuine 5x
// slowdown injected into the demo service and checks that
// internal/orchestrate (via internal/perf) flags it, with zero false
// positives on the no-op case. Per product spec section 7: "detect the
// injected 5x slowdown... with no false positives on noise-only PRs."
package harness

import (
	"context"
	"net/http"
	"time"

	"testing"

	"github.com/google/uuid"
	pb "github.com/jex2l/parity/internal/recording/pb"
	"github.com/jex2l/parity/internal/replay"

	orchestrate "github.com/jex2l/parity/internal/orchestrate"
)

func recordSlowExchange(t *testing.T, binPath string) *pb.Exchange {
	t.Helper()
	env := replay.NewProcessEnvironment("DEMO_ADDR", "/health")
	ep, err := env.Provision(context.Background(), replay.BuildRef{Name: "recording-baseline-slow", Cmd: binPath})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer env.Teardown(context.Background())

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(ep.BaseURL + "/slow")
	if err != nil {
		t.Fatalf("GET /slow: %v", err)
	}
	resp.Body.Close()

	return &pb.Exchange{
		ExchangeId: uuid.New().String(),
		Inbound:    &pb.Inbound{Method: "GET", Path: "/slow", PathTemplate: "/slow"},
		Response:   &pb.Response{Status: 200},
		Sampling:   &pb.Sampling{Weight: 1},
	}
}

func TestPerfRegression_Detects5xSlowdownNoFalsePositive(t *testing.T) {
	bin := buildDemoService(t)

	// Replay the /slow request many times per run so the latency
	// distributions have enough samples for Mann-Whitney U to be
	// meaningful (the single-shot mutation_test.go samples are fine for
	// structural diffing, but perf needs a real distribution).
	one := recordSlowExchange(t, bin)
	var exchanges []*pb.Exchange
	for i := 0; i < 20; i++ {
		// Build a fresh Exchange rather than copying *one by value: proto
		// messages embed a sync.Mutex internally, so a shallow struct copy
		// (caught by `go vet`) would be unsafe to use concurrently.
		exchanges = append(exchanges, &pb.Exchange{
			ExchangeId: uuid.New().String(),
			Inbound:    one.GetInbound(),
			Response:   one.GetResponse(),
			Sampling:   one.GetSampling(),
		})
	}

	newEnv := func() replay.Environment {
		return replay.NewProcessEnvironment("DEMO_ADDR", "/health")
	}

	t.Run("no_op_no_false_positive", func(t *testing.T) {
		cfg := orchestrate.Config{
			Base:        replay.BuildRef{Name: "base", Cmd: bin},
			Candidate:   replay.BuildRef{Name: "candidate", Cmd: bin}, // no mutation
			NewEnv:      newEnv,
			Concurrency: 4,
		}
		rep, err := orchestrate.Run(context.Background(), cfg, exchanges)
		if err != nil {
			t.Fatalf("orchestrate.Run: %v", err)
		}
		if len(rep.PerfRegressions) != 0 {
			t.Fatalf("expected 0 perf regressions on no-op candidate, got %+v", rep.PerfRegressions)
		}
	})

	t.Run("detects_injected_5x_slowdown", func(t *testing.T) {
		cfg := orchestrate.Config{
			Base:        replay.BuildRef{Name: "base", Cmd: bin},
			Candidate:   replay.BuildRef{Name: "candidate", Cmd: bin, Env: map[string]string{"PARITY_MUTATION": "slow_5x"}},
			NewEnv:      newEnv,
			Concurrency: 4,
		}
		rep, err := orchestrate.Run(context.Background(), cfg, exchanges)
		if err != nil {
			t.Fatalf("orchestrate.Run: %v", err)
		}
		if len(rep.PerfRegressions) == 0 {
			t.Fatalf("expected the injected 5x slowdown on /slow to be detected, got no perf regressions")
		}
		for _, r := range rep.PerfRegressions {
			t.Logf("regression: endpoint=%s base_p95=%.1fms candidate_p95=%.1fms effect=%.0f%% p=%.2e", r.Endpoint, r.BaseP95Ms, r.CandidateP95Ms, r.EffectPct*100, r.PValue)
		}
	})
}
