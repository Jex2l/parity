// Package privacy enforces parity.yaml's privacy.send_to_control_plane
// setting: a configurable gate on what may leave the customer's
// perimeter toward a hosted or shared control plane, per spec principle
// 2 ("configurable down to 'nothing leaves'").
package privacy

import "fmt"

// Mode is the send_to_control_plane setting.
type Mode string

const (
	ModeNone           Mode = "none"
	ModeDiffsOnly      Mode = "diffs_only"
	ModeDiffsAndSamples Mode = "diffs_and_samples"
)

// Payload is a classification of what a publisher wants to send. The
// Gate decides per-mode whether each kind is allowed through.
type PayloadKind string

const (
	KindDiffSummary PayloadKind = "diff_summary" // clusters, severities, counts — no raw request/response bodies
	KindRedactedSample PayloadKind = "redacted_sample" // a redacted example request/response body
	KindRawTraffic     PayloadKind = "raw_traffic" // unscrubbed production data — must never leave, in any mode
)

// Gate enforces Mode against attempted sends.
type Gate struct {
	Mode Mode
}

// NewGate validates mode and returns a Gate, or an error for an unknown
// mode (fail closed, not open, on misconfiguration).
func NewGate(mode string) (*Gate, error) {
	m := Mode(mode)
	switch m {
	case ModeNone, ModeDiffsOnly, ModeDiffsAndSamples:
		return &Gate{Mode: m}, nil
	case "":
		return &Gate{Mode: ModeNone}, nil // fail closed: unset means nothing leaves
	default:
		return nil, fmt.Errorf("privacy: unknown send_to_control_plane mode %q (want none|diffs_only|diffs_and_samples)", mode)
	}
}

// Allow reports whether kind may be sent under the gate's configured
// mode. KindRawTraffic is never allowed, regardless of mode — raw
// production data crossing the perimeter is not something any
// send_to_control_plane setting is meant to permit.
func (g *Gate) Allow(kind PayloadKind) bool {
	if kind == KindRawTraffic {
		return false
	}
	switch g.Mode {
	case ModeNone:
		return false
	case ModeDiffsOnly:
		return kind == KindDiffSummary
	case ModeDiffsAndSamples:
		return kind == KindDiffSummary || kind == KindRedactedSample
	default:
		return false
	}
}
