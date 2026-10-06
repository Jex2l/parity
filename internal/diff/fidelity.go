package diff

// Fidelity summarizes how faithfully base replay reproduced the originally
// recorded production response — the honesty mechanism from spec 6.4.
// Low fidelity means "don't trust this report's diffs", and the reason
// why must be surfaced, not buried.
type Fidelity struct {
	Total        int
	Reproduced   int // requests whose base replay matched the recorded response (after noise filtering)
	AuthFailures int
	Score        float64 // Reproduced / Total, in [0,1]
}

// LowFidelityThreshold below which a report must be marked low-confidence.
const LowFidelityThreshold = 0.8

// Compute derives a Fidelity summary from per-exchange results: for each
// exchange, wasReproduced is whether replaying it against base (after
// noise-model filtering) matched the recorded production response, and
// authFailed is whether the base replay returned 401/403 where production
// did not (a strong signal of broken auth during replay, not a real
// regression).
type ExchangeFidelity struct {
	Reproduced bool
	AuthFailed bool
}

func Compute(results []ExchangeFidelity) Fidelity {
	f := Fidelity{Total: len(results)}
	for _, r := range results {
		if r.Reproduced {
			f.Reproduced++
		}
		if r.AuthFailed {
			f.AuthFailures++
		}
	}
	if f.Total > 0 {
		f.Score = float64(f.Reproduced) / float64(f.Total)
	}
	return f
}

// IsLowConfidence reports whether the fidelity score is below the
// threshold at which a report's diffs should be flagged as unreliable.
func (f Fidelity) IsLowConfidence() bool {
	return f.Total > 0 && f.Score < LowFidelityThreshold
}

// Reason gives a human-readable explanation for low fidelity, prioritizing
// the most actionable cause.
func (f Fidelity) Reason() string {
	if !f.IsLowConfidence() {
		return ""
	}
	if f.Total > 0 && float64(f.AuthFailures)/float64(f.Total) > 0.2 {
		return "replay fidelity low: a large fraction of requests failed authentication during replay (test credentials likely missing or mismatched)"
	}
	return "replay fidelity low: base replay did not reproduce the recorded production response for a significant fraction of requests"
}
