// Package normalize canonicalizes JSON values for comparison: key order is
// irrelevant (maps), and helpers classify whether a given scalar looks
// like a timestamp, UUID, or other commonly-volatile shape. The learned
// per-path noise model lives in internal/diff, which uses these
// classifiers; this package only has the shape heuristics themselves so
// they can be unit tested in isolation.
package normalize

import (
	"bytes"
	"encoding/json"
	"regexp"
	"time"
)

// ParseJSON unmarshals body into a canonical Go value (map/slice/scalar
// tree). Non-JSON or empty bodies return (nil, false).
func ParseJSON(body []byte) (any, bool) {
	if len(body) == 0 {
		return nil, false
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

var (
	uuidRe      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	rfc3339Like = []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02"}
)

// LooksLikeUUID reports whether s is shaped like a UUID.
func LooksLikeUUID(s string) bool { return uuidRe.MatchString(s) }

// LooksLikeTimestamp reports whether s parses under any common timestamp
// format Parity treats as volatile-by-default.
func LooksLikeTimestamp(s string) bool {
	for _, layout := range rfc3339Like {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// KeyedArrayKeyField returns the field name used to key an array of
// objects for by-key diffing (e.g. "id"), or "" if the array doesn't look
// keyed (not all elements are objects with a shared identifying field).
func KeyedArrayKeyField(arr []any) string {
	if len(arr) == 0 {
		return ""
	}
	candidates := []string{"id", "ID", "Id"}
	for _, key := range candidates {
		allHave := true
		seen := map[string]bool{}
		for _, el := range arr {
			obj, ok := el.(map[string]any)
			if !ok {
				allHave = false
				break
			}
			v, ok := obj[key]
			if !ok {
				allHave = false
				break
			}
			s := scalarString(v)
			if seen[s] {
				allHave = false // duplicate key value; not safely keyable
				break
			}
			seen[s] = true
		}
		if allHave {
			return key
		}
	}
	return ""
}

func scalarString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
