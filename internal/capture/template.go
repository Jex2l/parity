package capture

import (
	"regexp"
	"strings"
)

var (
	numericSegment = regexp.MustCompile(`^\d+$`)
	// uuidSegment matches canonical UUIDs (with or without dashes stripped
	// not handled) — good enough for v1 templating.
	uuidSegment = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Templater turns a concrete request path into a stable template, e.g.
// "/users/123/orders/9" -> "/users/{id}/orders/{id}". It first consults any
// exact templates registered (e.g. from an imported OpenAPI spec), then
// falls back to heuristic segment classification.
type Templater struct {
	exact map[string]string // concrete path -> template, for exact OpenAPI-derived routes
}

// NewTemplater creates a Templater with no exact routes registered.
func NewTemplater() *Templater {
	return &Templater{exact: map[string]string{}}
}

// RegisterExact registers a known template for requests matching pattern
// exactly as given (used when importing OpenAPI path templates).
func (t *Templater) RegisterExact(pattern, template string) {
	t.exact[pattern] = template
}

// Template returns the inferred path template for path.
func (t *Templater) Template(path string) string {
	if tmpl, ok := t.exact[path]; ok {
		return tmpl
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		if numericSegment.MatchString(seg) {
			segments[i] = "{id}"
			continue
		}
		if uuidSegment.MatchString(seg) {
			segments[i] = "{id}"
			continue
		}
		if looksLikeOpaqueID(seg) {
			segments[i] = "{id}"
		}
	}
	return "/" + strings.Join(segments, "/")
}

// looksLikeOpaqueID flags long alphanumeric segments (e.g. "a1b2c3d4e5f6")
// that are plausibly resource identifiers rather than static path parts,
// using length + mixed alnum as a cheap heuristic.
func looksLikeOpaqueID(seg string) bool {
	if len(seg) < 8 {
		return false
	}
	hasDigit, hasAlpha := false, false
	for _, c := range seg {
		switch {
		case c >= '0' && c <= '9':
			hasDigit = true
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			hasAlpha = true
		default:
			return false // punctuation other than what we allow -> not opaque id
		}
	}
	return hasDigit && hasAlpha
}
