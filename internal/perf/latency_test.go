package perf

import (
	"testing"
	"time"
)

func dursMs(ms ...int) []time.Duration {
	out := make([]time.Duration, len(ms))
	for i, m := range ms {
		out[i] = time.Duration(m) * time.Millisecond
	}
	return out
}

func repeat(ms int, n int) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = time.Duration(ms) * time.Millisecond
	}
	return out
}

func TestMannWhitneyU_IdenticalDistributionsHighPValue(t *testing.T) {
	a := dursMs(10, 11, 9, 10, 12, 10, 9, 11, 10, 10, 11, 9, 10, 12, 10, 9, 11, 10, 10, 11)
	b := dursMs(10, 11, 9, 10, 12, 10, 9, 11, 10, 10, 11, 9, 10, 12, 10, 9, 11, 10, 10, 11)
	_, p := MannWhitneyU(a, b)
	if p < 0.5 {
		t.Fatalf("identical distributions should have a high p-value, got %v", p)
	}
}

func TestMannWhitneyU_ClearlyDifferentLowPValue(t *testing.T) {
	a := repeat(10, 30)
	b := repeat(50, 30)
	_, p := MannWhitneyU(a, b)
	if p > 0.01 {
		t.Fatalf("clearly different distributions should have a low p-value, got %v", p)
	}
}

func TestDetect_NoRegressionWithinNoiseFloor(t *testing.T) {
	baseA := repeat(10, 30)
	baseB := repeat(13, 30)    // natural run-to-run variance of 3ms (the noise floor)
	candidate := repeat(12, 30) // only 2ms slower than base-A: less than the 3ms noise floor
	cfg := DefaultRegressionConfig()
	_, found := Detect("/slow", baseA, baseB, candidate, cfg)
	if found {
		t.Fatalf("candidate within the A-vs-B noise floor must not be flagged as a regression")
	}
}

func TestDetect_FlagsInjected5xSlowdown(t *testing.T) {
	baseA := repeat(50, 30)
	baseB := repeat(51, 30)
	candidate := repeat(250, 30) // 5x slower, far beyond noise floor
	cfg := DefaultRegressionConfig()
	reg, found := Detect("/slow", baseA, baseB, candidate, cfg)
	if !found {
		t.Fatalf("expected the 5x slowdown to be detected")
	}
	if reg.EffectPct < 1.0 {
		t.Fatalf("expected a >100%% effect size for a 5x slowdown, got %v", reg.EffectPct)
	}
	t.Logf("regression: %+v", reg)
}
