// Package orchestrate ties replay, diff, and report together into the
// three-way comparison described in the product spec: base-A vs base-B
// gives the noise floor; base-A vs candidate gives signal. Used by both
// cmd/parity and the eval harness, so the mutation-scoring tests exercise
// the exact same code path a real `parity replay` run would.
package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jex2l/parity/internal/diff"
	"github.com/jex2l/parity/internal/perf"
	"github.com/jex2l/parity/internal/recording"
	pb "github.com/jex2l/parity/internal/recording/pb"
	"github.com/jex2l/parity/internal/replay"
	"github.com/jex2l/parity/internal/report"
	"github.com/jex2l/parity/internal/scrub"
)

// Config describes one three-way comparison run.
type Config struct {
	Base      replay.BuildRef
	Candidate replay.BuildRef
	NewEnv    func() replay.Environment // factory so base-A, base-B, candidate each get an isolated environment
	Concurrency int
	// Scrubber, if set, is applied to every live replay response body
	// before it's compared against anything — including the recorded
	// response, which (if it came from a real `parity-capture` segment)
	// was already scrubbed at capture time. Without this, every
	// PII-bearing field looks like a spurious regression, because a raw
	// live value never matches its own pseudonymized recorded form. Pass
	// the same Scrubber (same HMAC secret) that produced the segment.
	// See docs/limits.md's former "Phase 2" entry on this — fixed here.
	Scrubber *scrub.Scrubber
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

	if cfg.Scrubber != nil {
		scrubResults(cfg.Scrubber, resultsA)
		scrubResults(cfg.Scrubber, resultsB)
		scrubResults(cfg.Scrubber, resultsC)
	}

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
	perfRegressions := detectPerfRegressions(exchanges, resultsA, resultsB, resultsC)

	return report.Report{
		TotalReplayed:   len(exchanges),
		Fidelity: report.FidelitySummary{
			Score:         fidelity.Score,
			LowConfidence: fidelity.IsLowConfidence(),
			Reason:        fidelity.Reason(),
		},
		Clusters:        report.FromClusters(clusters),
		AbsorbedCount:   absorbedTotal,
		SignalCount:     signalTotal,
		PerfRegressions: perfRegressions,
	}, nil
}

// detectPerfRegressions groups per-exchange replay durations by endpoint
// template and runs perf.Detect on each group, per spec 6.6: only the
// A-vs-B noise floor, not raw variance, gates what counts as a regression.
func detectPerfRegressions(exchanges []*pb.Exchange, resultsA, resultsB, resultsC []replay.ExchangeResult) []report.PerfRegressionView {
	type durs struct{ a, b, c []time.Duration }
	byEndpoint := map[string]*durs{}
	for i := range exchanges {
		ep := resultsA[i].Endpoint
		d, ok := byEndpoint[ep]
		if !ok {
			d = &durs{}
			byEndpoint[ep] = d
		}
		d.a = append(d.a, resultsA[i].Duration)
		d.b = append(d.b, resultsB[i].Duration)
		d.c = append(d.c, resultsC[i].Duration)
	}
	cfg := perf.DefaultRegressionConfig()
	var out []report.PerfRegressionView
	for ep, d := range byEndpoint {
		reg, found := perf.Detect(ep, d.a, d.b, d.c, cfg)
		if !found {
			continue
		}
		out = append(out, report.PerfRegressionView{
			Endpoint:       reg.Endpoint,
			BaseP95Ms:      float64(reg.BaseP95.Microseconds()) / 1000,
			CandidateP95Ms: float64(reg.CandidateP95.Microseconds()) / 1000,
			EffectPct:      reg.EffectPct,
			PValue:         reg.PValue,
		})
	}
	return out
}

// scrubResults applies scrubber to every result's body in place.
func scrubResults(scrubber *scrub.Scrubber, results []replay.ExchangeResult) {
	for i := range results {
		results[i].Body = scrubber.ScrubBody(results[i].Body)
	}
}

// LoadExchanges reads up to limit exchanges from a recording reader
// (limit<=0 means all). A segment that ends mid-record (ErrTruncated)
// is not treated as fatal: whatever was read before the truncation point
// is returned, matching internal/recording's own stance that earlier
// records in a truncated segment remain valid and must not be thrown
// away just because the tail is damaged. Any other error is fatal.
func LoadExchanges(next func() (*pb.Exchange, error), limit int) ([]*pb.Exchange, error) {
	var out []*pb.Exchange
	for limit <= 0 || len(out) < limit {
		ex, err := next()
		if err == io.EOF {
			break
		}
		if errors.Is(err, recording.ErrTruncated) {
			break
		}
		if err != nil {
			return out, err
		}
		out = append(out, ex)
	}
	return out, nil
}
