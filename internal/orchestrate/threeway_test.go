package orchestrate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jex2l/parity/internal/recording"
	pb "github.com/jex2l/parity/internal/recording/pb"
	"github.com/jex2l/parity/internal/replay"
	"github.com/jex2l/parity/internal/scrub"
)

// httpEnv adapts an already-running httptest.Server to replay.Environment
// so this test doesn't need a real subprocess build — it's exercising
// orchestrate's scrubbing logic, not process provisioning (that's covered
// elsewhere, e.g. eval/harness).
type httpEnv struct{ url string }

func (e *httpEnv) Provision(ctx context.Context, build replay.BuildRef) (replay.Endpoint, error) {
	return replay.Endpoint{BaseURL: e.url}, nil
}
func (e *httpEnv) Reset(ctx context.Context) error    { return nil }
func (e *httpEnv) Teardown(ctx context.Context) error { return nil }

// TestRun_ScrubberFixesPIIFalsePositive is the regression test for the
// bug documented in docs/limits.md: a recorded (scrubbed) response
// containing a pseudonymized email must not look like a diff against a
// live replay that returns the real, unscrubbed email — once the same
// Scrubber is applied to the live side too.
func TestRun_ScrubberFixesPIIFalsePositive(t *testing.T) {
	const realEmail = "real.user@example.com"
	mux := http.NewServeMux()
	mux.HandleFunc("/users/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"email": realEmail})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	scrubber := scrub.New(scrub.Config{HMACSecret: []byte("test-secret")})
	scrubbedBody, _ := json.Marshal(map[string]string{"email": realEmail})
	scrubbedBody = scrubber.ScrubBody(scrubbedBody)

	// This is what a real capture-produced segment would contain: the
	// recorded response already pseudonymized.
	exchanges := []*pb.Exchange{
		{
			ExchangeId: "ex-1",
			Inbound:    &pb.Inbound{Method: "GET", Path: "/users/1", PathTemplate: "/users/{id}"},
			Response:   &pb.Response{Status: 200, Body: scrubbedBody},
			Sampling:   &pb.Sampling{Weight: 1},
		},
	}

	newEnv := func() replay.Environment { return &httpEnv{url: srv.URL} }

	t.Run("without_scrubber_looks_like_a_false_positive", func(t *testing.T) {
		cfg := Config{
			Base:        replay.BuildRef{Name: "base"},
			Candidate:   replay.BuildRef{Name: "candidate"},
			NewEnv:      newEnv,
			Concurrency: 2,
			// No Scrubber set — reproduces the historical bug.
		}
		rep, err := Run(context.Background(), cfg, exchanges)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Fidelity.Score >= 1.0 {
			t.Fatalf("expected the unscrubbed comparison to show LOW fidelity (the bug), got score=%.2f", rep.Fidelity.Score)
		}
		t.Logf("without scrubber: fidelity=%.2f (demonstrates the bug)", rep.Fidelity.Score)
	})

	t.Run("with_scrubber_fidelity_is_restored", func(t *testing.T) {
		cfg := Config{
			Base:        replay.BuildRef{Name: "base"},
			Candidate:   replay.BuildRef{Name: "candidate"},
			NewEnv:      newEnv,
			Concurrency: 2,
			Scrubber:    scrubber,
		}
		rep, err := Run(context.Background(), cfg, exchanges)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Fidelity.Score != 1.0 {
			t.Fatalf("expected fidelity=1.0 once both sides are consistently scrubbed, got %.2f", rep.Fidelity.Score)
		}
		if len(rep.Clusters) != 0 {
			t.Fatalf("expected zero diff clusters once PII pseudonymization is consistent, got %+v", rep.Clusters)
		}
	})
}

// TestLoadExchanges_TruncatedSegmentReturnsPartialResults proves the
// other robustness fix: a segment that ends mid-record must not make
// `parity replay` fail outright — the records read before the
// truncation point are still valid and should be used.
func TestLoadExchanges_TruncatedSegmentReturnsPartialResults(t *testing.T) {
	served := 0
	next := func() (*pb.Exchange, error) {
		served++
		if served <= 3 {
			return &pb.Exchange{ExchangeId: fmt.Sprintf("ex-%d", served)}, nil
		}
		return nil, fmt.Errorf("%w: simulated truncation", recording.ErrTruncated)
	}
	out, err := LoadExchanges(next, 0)
	if err != nil {
		t.Fatalf("expected a truncated segment to not be a fatal error, got %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected the 3 valid records before truncation, got %d", len(out))
	}
}
