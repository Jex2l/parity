// Package orchestrate ties replay, diff, and report together into the
// three-way comparison described in the product spec: base-A vs base-B
// gives the noise floor; base-A vs candidate gives signal. Used by both
// cmd/parity and the eval harness, so the mutation-scoring tests exercise
// the exact same code path a real `parity replay` run would.
package orchestrate

import (
	"context"
	"fmt"
	"io"

	"github.com/jex2l/parity/internal/diff"
	pb "github.com/jex2l/parity/internal/recording/pb"
	"github.com/jex2l/parity/internal/replay"
	"github.com/jex2l/parity/internal/report"
)

// Config describes one three-way comparison run.
type Config struct {
	Base      replay.BuildRef
	Candidate replay.BuildRef
	NewEnv    func() replay.Environment // factory so base-A, base-B, candidate each get an isolated environment
	Concurrency int
}

// Run provisions base-A, base-B, and candidate, replays exchanges against
// each, learns the per-endpoint noise model from A vs B, diffs A vs
// candidate through that model, and returns the assembled Report plus the
// raw per-exchange fidelity data (useful for eval scoring).
func Run(ctx context.Context, cfg Config, exchanges []*pb.Exchange) (report.Report, error) {
	envA := cfg.NewEnv()
	envB := cfg.NewEnv()
	envC := cfg.NewEnv()
	defer envA.Teardown(ctx)
	defer envB.Teardown(ctx)
	defer envC.Teardown(ctx)

	epA, err := envA.Provision(ctx, cfg.Base)
	if err != nil {
		return report.Report{}, fmt.Errorf("orchestrate: provision base-A: %w", err)
	}
	baseNameB := cfg.Base
	baseNameB.Name = "base-B"
	epB, err := envB.Provision(ctx, baseNameB)
	if err != nil {
		return report.Report{}, fmt.Errorf("orchestrate: provision base-B: %w", err)
	}
	epC, err := envC.Provision(ctx, cfg.Candidate)
	if err != nil {
		return report.Report{}, fmt.Errorf("orchestrate: provision candidate: %w", err)
	}

	sched := replay.NewScheduler(cfg.Concurrency)
	resultsA := sched.Replay(ctx, epA, exchanges)
	resultsB := sched.Replay(ctx, epB, exchanges)
	resultsC := sched.Replay(ctx, epC, exchanges)

	// Group A/B results per endpoint template to learn one noise model per
	// endpoint (volatility is endpoint-specific: a list endpoint's order
	// instability doesn't tell you anything about a detail endpoint).
	abByEndpoint := map[string][]diff.Result{}
	for i := range exchanges {
		ep := resultsA[i].Endpoint
		abByEndpoint[ep] = append(abByEndpoint[ep], diff.Compare(resultsA[i].Status, resultsB[i].Status, resultsA[i].Body, resultsB[i].Body))
	}
	models := map[string]diff.NoiseModel{}
	for ep, pairs := range abByEndpoint {
		models[ep] = diff.Learn(pairs)
	}

	var exchangeDiffs []diff.ExchangeDiff
	var fidelityInputs []diff.ExchangeFidelity
	absorbedTotal, signalTotal := 0, 0

	for i, ex := range exchanges {
		ep := resultsA[i].Endpoint
		model := models[ep]

		// Fidelity: did base-A reproduce the originally recorded response?
		recorded := ex.GetResponse()
		fidelityDiff := diff.Compare(int(recorded.GetStatus()), resultsA[i].Status, recorded.GetBody(), resultsA[i].Body)
		fSignal, _ := diff.Filter(fidelityDiff.Changes, model)
		authFailed := recorded.GetStatus() < 400 && (resultsA[i].Status == 401 || resultsA[i].Status == 403)
		fidelityInputs = append(fidelityInputs, diff.ExchangeFidelity{
			Reproduced: len(fSignal) == 0 && int(recorded.GetStatus()) == resultsA[i].Status,
			AuthFailed: authFailed,
		})

		// Signal: does candidate differ from base-A beyond the noise floor?
		candidateDiff := diff.Compare(resultsA[i].Status, resultsC[i].Status, resultsA[i].Body, resultsC[i].Body)
		signal, absorbed := diff.Filter(candidateDiff.Changes, model)
		absorbedTotal += len(absorbed)
		signalTotal += len(signal)

		exchangeDiffs = append(exchangeDiffs, diff.ExchangeDiff{
			ExchangeID: ex.GetExchangeId(),
			Endpoint:   ep,
			Signal:     signal,
			Absorbed:   absorbed,
			Weight:     ex.GetSampling().GetWeight(),
		})
	}

	fidelity := diff.Compute(fidelityInputs)
	clusters := diff.BuildClusters(exchangeDiffs)

	return report.Report{
		TotalReplayed: len(exchanges),
		Fidelity: report.FidelitySummary{
			Score:         fidelity.Score,
			LowConfidence: fidelity.IsLowConfidence(),
			Reason:        fidelity.Reason(),
		},
		Clusters:      report.FromClusters(clusters),
		AbsorbedCount: absorbedTotal,
		SignalCount:   signalTotal,
	}, nil
}

// LoadExchanges reads up to limit exchanges from a recording reader
// (limit<=0 means all).
func LoadExchanges(next func() (*pb.Exchange, error), limit int) ([]*pb.Exchange, error) {
	var out []*pb.Exchange
	for limit <= 0 || len(out) < limit {
		ex, err := next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		out = append(out, ex)
	}
	return out, nil
}
