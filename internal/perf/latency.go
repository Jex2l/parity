// Package perf measures per-endpoint latency distributions and detects
// regressions using the base-A vs base-B run as a noise floor, per
// product spec section 6.6.
package perf

import (
	"math"
	"sort"
	"time"
)

// Percentile returns the p-th percentile (0..1) of samples.
func Percentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(p * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

// Stats summarizes a latency distribution.
type Stats struct {
	N   int
	P50 time.Duration
	P90 time.Duration
	P95 time.Duration
	P99 time.Duration
}

// Summarize computes Stats for samples.
func Summarize(samples []time.Duration) Stats {
	return Stats{
		N:   len(samples),
		P50: Percentile(samples, 0.50),
		P90: Percentile(samples, 0.90),
		P95: Percentile(samples, 0.95),
		P99: Percentile(samples, 0.99),
	}
}

// MannWhitneyU computes the U statistic and a two-sided p-value (normal
// approximation, valid for n,m >= ~20, which the replay sample sizes this
// product targets always exceed) for the null hypothesis that a and b are
// drawn from the same distribution. Smaller p means more evidence they
// differ.
func MannWhitneyU(a, b []time.Duration) (u, pValue float64) {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return 0, 1
	}
	type labeled struct {
		v   time.Duration
		grp int // 0 = a, 1 = b
	}
	all := make([]labeled, 0, n+m)
	for _, v := range a {
		all = append(all, labeled{v, 0})
	}
	for _, v := range b {
		all = append(all, labeled{v, 1})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v < all[j].v })

	ranks := make([]float64, len(all))
	i := 0
	for i < len(all) {
		j := i
		for j < len(all) && all[j].v == all[i].v {
			j++
		}
		avgRank := float64(i+j+1) / 2 // 1-based average rank for ties
		for k := i; k < j; k++ {
			ranks[k] = avgRank
		}
		i = j
	}

	rankSumA := 0.0
	for i, l := range all {
		if l.grp == 0 {
			rankSumA += ranks[i]
		}
	}
	uA := rankSumA - float64(n*(n+1))/2
	meanU := float64(n*m) / 2
	stdU := math.Sqrt(float64(n*m*(n+m+1)) / 12)
	if stdU == 0 {
		return uA, 1
	}
	z := (uA - meanU) / stdU
	pValue = 2 * (1 - normalCDF(math.Abs(z)))
	return uA, pValue
}

func normalCDF(x float64) float64 {
	return 0.5 * (1 + math.Erf(x/math.Sqrt2))
}

// RegressionConfig gates when a latency difference is reported, per spec
// 6.6: require both statistical significance and a minimum effect size.
type RegressionConfig struct {
	Alpha        float64       // significance threshold, e.g. 0.05
	MinEffectPct float64       // minimum relative p95 increase, e.g. 0.15
	MinEffectMs  time.Duration // minimum absolute p95 increase, e.g. 10ms
}

// DefaultRegressionConfig matches the spec's stated defaults.
func DefaultRegressionConfig() RegressionConfig {
	return RegressionConfig{Alpha: 0.05, MinEffectPct: 0.15, MinEffectMs: 10 * time.Millisecond}
}

// Regression describes a detected latency regression for one endpoint.
type Regression struct {
	Endpoint       string
	BaseP95        time.Duration
	CandidateP95   time.Duration
	NoiseFloorP95  time.Duration // |baseA.P95 - baseB.P95|, the observed nondeterminism
	PValue         float64
	EffectPct      float64
}

// Detect compares baseA (vs baseB as the noise floor) to candidate and
// reports a Regression only if the candidate is significantly slower than
// baseA by more than both the configured minimums AND the A-vs-B noise
// floor (so ordinary run-to-run variance is never flagged).
func Detect(endpoint string, baseA, baseB, candidate []time.Duration, cfg RegressionConfig) (Regression, bool) {
	statsA := Summarize(baseA)
	statsB := Summarize(baseB)
	statsC := Summarize(candidate)

	noiseFloor := statsB.P95 - statsA.P95
	if noiseFloor < 0 {
		noiseFloor = -noiseFloor
	}

	effect := statsC.P95 - statsA.P95
	if effect <= 0 {
		return Regression{}, false
	}
	if effect <= noiseFloor {
		return Regression{}, false // within observed run-to-run variance
	}

	effectPct := 0.0
	if statsA.P95 > 0 {
		effectPct = float64(effect) / float64(statsA.P95)
	}
	if effectPct < cfg.MinEffectPct && effect < cfg.MinEffectMs {
		return Regression{}, false
	}

	_, pValue := MannWhitneyU(candidate, baseA)
	if pValue > cfg.Alpha {
		return Regression{}, false
	}

	return Regression{
		Endpoint:      endpoint,
		BaseP95:       statsA.P95,
		CandidateP95:  statsC.P95,
		NoiseFloorP95: noiseFloor,
		PValue:        pValue,
		EffectPct:     effectPct,
	}, true
}
