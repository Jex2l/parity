package diff

import (
	"strconv"

	"github.com/jex2l/parity/internal/normalize"
)

// NoiseClass is how a given JSON path has been observed to vary across
// baseline-vs-baseline (A vs B) replays of the same build.
type NoiseClass string

const (
	Stable               NoiseClass = "STABLE"
	AlwaysDifferentOpaque NoiseClass = "ALWAYS_DIFFERENT_OPAQUE"
	TimestampLike        NoiseClass = "TIMESTAMP_LIKE"
	OrderUnstable        NoiseClass = "ORDER_UNSTABLE"
	FloatJitter          NoiseClass = "FLOAT_JITTER"
)

// NoiseModel maps a JSON path to its learned class for one endpoint
// template. It is learned from Changes observed between baseline-A and
// baseline-B replays (same build, same requests) — differences there are
// definitionally noise, not signal.
type NoiseModel map[string]NoiseClass

// Learn builds a NoiseModel from a set of base-A vs base-B diff Results
// for a single endpoint template. Any path that ever differs between A
// and B is classified; paths that never differ are simply absent (treated
// as STABLE by callers via the zero value).
func Learn(abResults []Result) NoiseModel {
	model := NoiseModel{}
	for _, r := range abResults {
		for _, c := range r.Changes {
			if existing, ok := model[c.Path]; ok && existing != Stable {
				continue // already classified from an earlier pair
			}
			model[c.Path] = classify(c)
		}
	}
	return model
}

func classify(c Change) NoiseClass {
	switch c.Type {
	case ArrayOrderChanged:
		return OrderUnstable
	case ValueChanged:
		bs, bOK := c.Before.(string)
		as, aOK := c.After.(string)
		if bOK && aOK {
			if normalize.LooksLikeTimestamp(bs) && normalize.LooksLikeTimestamp(as) {
				return TimestampLike
			}
			if normalize.LooksLikeUUID(bs) && normalize.LooksLikeUUID(as) {
				return AlwaysDifferentOpaque
			}
		}
		if bf, bOK := asFloat(c.Before); bOK {
			if af, aOK := asFloat(c.After); aOK {
				if bf != af {
					return FloatJitter
				}
			}
		}
		return AlwaysDifferentOpaque // unexplained but observed-volatile; absorb conservatively
	default:
		return Stable
	}
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

const floatJitterTolerance = 0.02 // 2% relative tolerance, learned-or-default for v1

// Filter splits candidate-vs-baseline changes into signal (not explained
// by the noise model) and absorbed (explained — this is nondeterminism
// the model already knows about, not a real behavioral difference).
func Filter(changes []Change, model NoiseModel) (signal, absorbed []Change) {
	for _, c := range changes {
		class, known := model[c.Path]
		if !known {
			signal = append(signal, c)
			continue
		}
		if absorbsChange(class, c) {
			absorbed = append(absorbed, c)
		} else {
			signal = append(signal, c)
		}
	}
	return signal, absorbed
}

func absorbsChange(class NoiseClass, c Change) bool {
	switch class {
	case Stable:
		return false
	case AlwaysDifferentOpaque, TimestampLike:
		// Any VALUE_CHANGED at this path is expected volatility; a
		// TYPE_CHANGED or FIELD_REMOVED is still signal (format changed).
		return c.Type == ValueChanged
	case OrderUnstable:
		return c.Type == ArrayOrderChanged
	case FloatJitter:
		if c.Type != ValueChanged {
			return false
		}
		bf, bOK := asFloat(c.Before)
		af, aOK := asFloat(c.After)
		if !bOK || !aOK {
			return false
		}
		if bf == 0 {
			return af == 0
		}
		rel := (af - bf) / bf
		if rel < 0 {
			rel = -rel
		}
		return rel <= floatJitterTolerance
	default:
		return false
	}
}
