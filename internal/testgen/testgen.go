// Package testgen distills recorded exchanges into a portable,
// noise-aware test case format (spec section 6.8), and can execute that
// format against a running build.
package testgen

import (
	"fmt"

	"github.com/jex2l/parity/internal/normalize"
	pb "github.com/jex2l/parity/internal/recording/pb"
)

// Case is one portable test case. It is deliberately JSON/YAML-friendly
// (exported fields, simple types) so it can be marshaled directly.
type Case struct {
	Name           string `json:"name" yaml:"name"`
	Method         string `json:"method" yaml:"method"`
	Path           string `json:"path" yaml:"path"`
	Query          string `json:"query,omitempty" yaml:"query,omitempty"`
	ExpectedStatus int    `json:"expected_status" yaml:"expected_status"`
	// ExpectedBody is the recorded response body with volatile values
	// replaced by matcher tokens (<ANY_UUID>, <ANY_TIMESTAMP>) so the
	// generated test doesn't flake on legitimate nondeterminism.
	ExpectedBody string `json:"expected_body" yaml:"expected_body"`
	Provenance   string `json:"provenance" yaml:"provenance"` // which exchange this came from, for traceability — never raw PII
}

// Generate selects a minimal diverse set of exchanges and converts each
// into a Case. Diversity v1: one case per distinct (endpoint template,
// status code) pair, preferring the first exchange seen for each, plus
// every exchange explicitly passed in `mustInclude` (e.g. ones that
// exposed a regression this run — spec 6.8: "these become the most
// important tests").
func Generate(exchanges []*pb.Exchange, mustInclude map[string]bool) []Case {
	seen := map[string]bool{}
	var cases []Case

	add := func(ex *pb.Exchange, reason string) {
		cases = append(cases, toCase(ex, reason))
	}

	for _, ex := range exchanges {
		key := fmt.Sprintf("%s|%d", ex.GetInbound().GetPathTemplate(), ex.GetResponse().GetStatus())
		if mustInclude[ex.GetExchangeId()] {
			add(ex, "exposed a regression this run")
			seen[key] = true
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		add(ex, "distinct endpoint+status coverage")
	}
	return cases
}

func toCase(ex *pb.Exchange, reason string) Case {
	in := ex.GetInbound()
	resp := ex.GetResponse()
	return Case{
		Name:           fmt.Sprintf("%s_%s_%d", sanitize(in.GetMethod()), sanitize(in.GetPathTemplate()), resp.GetStatus()),
		Method:         in.GetMethod(),
		Path:           in.GetPath(),
		Query:          in.GetQuery(),
		ExpectedStatus: int(resp.GetStatus()),
		ExpectedBody:   MaskVolatile(string(resp.GetBody())),
		Provenance:     fmt.Sprintf("exchange %s (%s)", ex.GetExchangeId(), reason),
	}
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// MaskVolatile replaces UUID- and timestamp-shaped JSON string values
// with matcher tokens. It operates on the already-parsed-and-reserialized
// form so matching is structural, not textual.
func MaskVolatile(body string) string {
	v, ok := normalize.ParseJSON([]byte(body))
	if !ok {
		return body
	}
	masked := maskValue(v)
	b, err := marshalCompact(masked)
	if err != nil {
		return body
	}
	return string(b)
}

func maskValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			out[k] = maskValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = maskValue(val)
		}
		return out
	case string:
		if normalize.LooksLikeUUID(t) {
			return "<ANY_UUID>"
		}
		if normalize.LooksLikeTimestamp(t) {
			return "<ANY_TIMESTAMP>"
		}
		return t
	default:
		return v
	}
}
