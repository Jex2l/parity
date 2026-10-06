package privacy

import "testing"

func TestGate_ModeNoneBlocksEverything(t *testing.T) {
	g, err := NewGate("none")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []PayloadKind{KindDiffSummary, KindRedactedSample, KindRawTraffic} {
		if g.Allow(k) {
			t.Fatalf("mode=none must block %s", k)
		}
	}
}

func TestGate_ModeDiffsOnly(t *testing.T) {
	g, _ := NewGate("diffs_only")
	if !g.Allow(KindDiffSummary) {
		t.Fatal("diffs_only must allow diff summaries")
	}
	if g.Allow(KindRedactedSample) {
		t.Fatal("diffs_only must block redacted samples")
	}
	if g.Allow(KindRawTraffic) {
		t.Fatal("diffs_only must block raw traffic")
	}
}

func TestGate_ModeDiffsAndSamples(t *testing.T) {
	g, _ := NewGate("diffs_and_samples")
	if !g.Allow(KindDiffSummary) || !g.Allow(KindRedactedSample) {
		t.Fatal("diffs_and_samples must allow both diff summaries and redacted samples")
	}
	if g.Allow(KindRawTraffic) {
		t.Fatal("raw traffic must never be allowed, in any mode")
	}
}

func TestGate_UnknownModeFailsClosed(t *testing.T) {
	if _, err := NewGate("send_everything_please"); err == nil {
		t.Fatal("expected an error for an unrecognized mode, not a silent fallback")
	}
}

func TestGate_EmptyModeDefaultsToNone(t *testing.T) {
	g, err := NewGate("")
	if err != nil {
		t.Fatalf("empty mode should default to none, not error: %v", err)
	}
	if g.Allow(KindDiffSummary) {
		t.Fatal("unset mode must fail closed (nothing leaves)")
	}
}
