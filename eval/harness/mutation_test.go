// mutation_test.go is the Phase 2 scoring harness: for every entry in
// eval/mutations.Catalog, it runs the real three-way comparison
// (internal/orchestrate.Run) against the real demo service and checks
// Parity's report against the catalog's ground truth. This is the
// "evaluation harness" deliverable from product spec section 7, scoped to
// Phase 2's mutation set (see docs/plans/phase-2.md for why the catalog is
// smaller than the full spec's list).
package harness

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jex2l/parity/eval/mutations"
	pb "github.com/jex2l/parity/internal/recording/pb"
	"github.com/jex2l/parity/internal/replay"

	orchestrate "github.com/jex2l/parity/internal/orchestrate"
)

// buildDemoService compiles eval/demo-service once for the whole test
// binary and returns the path to the resulting executable.
func buildDemoService(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "demo-service")
	cmd := exec.Command("go", "build", "-o", bin, "../demo-service")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building demo-service: %v\n%s", err, out)
	}
	return bin
}

// recordBaseline captures what the unmutated demo service actually
// returns for a fixed request set, standing in for "recorded production
// traffic" without needing the full Phase 1 capture pipeline in this test.
func recordBaseline(t *testing.T, binPath string) []*pb.Exchange {
	t.Helper()
	env := replay.NewProcessEnvironment("DEMO_ADDR", "/health")
	ep, err := env.Provision(context.Background(), replay.BuildRef{Name: "recording-baseline", Cmd: binPath})
	if err != nil {
		t.Fatalf("provision recording baseline: %v", err)
	}
	defer env.Teardown(context.Background())

	requests := []struct {
		method, path, query string
	}{
		{"GET", "/orders/3", ""},
		{"GET", "/orders/17", ""},
		{"GET", "/orders/42", ""},
		{"GET", "/orders/99", ""},
		{"GET", "/orders/150", ""},
		{"GET", "/users", "page=1&page_size=10"},
		{"GET", "/users", "page=2&page_size=10"},
		{"GET", "/users", "page=3&page_size=5"},
	}

	client := &http.Client{Timeout: 5 * time.Second}
	var exchanges []*pb.Exchange
	for _, r := range requests {
		url := ep.BaseURL + r.path
		if r.query != "" {
			url += "?" + r.query
		}
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("recording request %s: %v", url, err)
		}
		body := make([]byte, 0, 1024)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		resp.Body.Close()

		exchanges = append(exchanges, &pb.Exchange{
			ExchangeId: uuid.New().String(),
			Inbound: &pb.Inbound{
				Method:       r.method,
				Path:         r.path,
				PathTemplate: templateOf(r.path),
				Query:        r.query,
			},
			Response: &pb.Response{
				Status: int32(resp.StatusCode),
				Body:   body,
			},
			Sampling: &pb.Sampling{Weight: 1},
		})
	}
	return exchanges
}

func templateOf(path string) string {
	switch {
	case path == "/users":
		return "/users"
	default:
		return "/orders/{id}"
	}
}

// TestMutationCatalog_DetectionAndFalsePositives runs every catalog entry
// through the real three-way comparison and scores detection recall on
// unintended mutations plus the false-positive rate on the no-op case,
// per spec section 7(5): target >=80% detection (Phase 2 acceptance),
// zero false positives on the no-op/clean case.
func TestMutationCatalog_DetectionAndFalsePositives(t *testing.T) {
	bin := buildDemoService(t)
	exchanges := recordBaseline(t, bin)

	newEnv := func() replay.Environment {
		return replay.NewProcessEnvironment("DEMO_ADDR", "/health")
	}

	detected, unintendedTotal := 0, 0
	falsePositives := 0

	for _, m := range mutations.Catalog {
		t.Run(m.Name, func(t *testing.T) {
			cfg := orchestrate.Config{
				Base:        replay.BuildRef{Name: "base", Cmd: bin},
				Candidate:   replay.BuildRef{Name: "candidate", Cmd: bin, Env: map[string]string{"PARITY_MUTATION": m.EnvValue}},
				NewEnv:      newEnv,
				Concurrency: 4,
			}
			rep, err := orchestrate.Run(context.Background(), cfg, exchanges)
			if err != nil {
				t.Fatalf("orchestrate.Run: %v", err)
			}

			hasSignal := len(rep.Clusters) > 0
			t.Logf("%s: clusters=%d fidelity=%.2f absorbed=%d signal=%d", m.Name, len(rep.Clusters), rep.Fidelity.Score, rep.AbsorbedCount, rep.SignalCount)
			for _, c := range rep.Clusters {
				t.Logf("  cluster: endpoint=%s paths=%v types=%v severity=%s count=%d", c.Endpoint, c.ChangedPaths, c.ChangeTypes, c.Severity, c.Count)
			}

			switch m.Expect {
			case mutations.ExpectNoOp:
				if hasSignal {
					falsePositives++
					t.Errorf("no-op mutation produced %d signal cluster(s), want 0 (noise model failed to absorb something, or a real bug)", len(rep.Clusters))
				}
			case mutations.ExpectUnintended:
				unintendedTotal++
				if hasSignal {
					detected++
				} else {
					t.Errorf("unintended mutation %q produced no signal clusters — MISSED detection", m.Name)
				}
			case mutations.ExpectIntendedOrClean:
				// Phase 2 does not classify intent yet (that's Phase 4);
				// this just checks the run completes and doesn't crash or
				// tank fidelity, which it would if the diff engine choked
				// on the new field.
				if rep.Fidelity.LowConfidence {
					t.Errorf("intended-change mutation %q unexpectedly produced low replay fidelity", m.Name)
				}
			}
		})
	}

	if unintendedTotal == 0 {
		t.Fatal("no unintended mutations in catalog — test is vacuous")
	}
	recall := float64(detected) / float64(unintendedTotal)
	t.Logf("detection recall on unintended mutations: %.0f%% (%d/%d)", recall*100, detected, unintendedTotal)
	if recall < 0.8 {
		t.Fatalf("detection recall %.2f below Phase 2 acceptance target of 0.80", recall)
	}
	if falsePositives > 0 {
		t.Fatalf("%d false positive(s) on no-op mutations, want 0", falsePositives)
	}
}
