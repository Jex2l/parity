// Package diff implements the structural JSON-aware diff between two
// responses, the learned noise model that distinguishes nondeterminism
// from real behavioral signal, severity scoring, and clustering.
package diff

import (
	"fmt"
	"sort"

	"github.com/jex2l/parity/internal/normalize"
)

// ChangeType enumerates the kinds of structural differences diff can
// produce, matching the taxonomy in the product spec section 6.5.
type ChangeType string

const (
	FieldAdded          ChangeType = "FIELD_ADDED"
	FieldRemoved        ChangeType = "FIELD_REMOVED"
	TypeChanged         ChangeType = "TYPE_CHANGED"
	ValueChanged        ChangeType = "VALUE_CHANGED"
	ArrayLengthChanged  ChangeType = "ARRAY_LENGTH_CHANGED"
	ArrayOrderChanged   ChangeType = "ARRAY_ORDER_CHANGED"
	StatusChanged       ChangeType = "STATUS_CHANGED"
	ErrorAppeared       ChangeType = "ERROR_APPEARED"
)

// Change is one typed difference at a JSON path.
type Change struct {
	Path   string
	Type   ChangeType
	Before any
	After  any
}

// Result is the full diff between two HTTP responses (status + body).
type Result struct {
	StatusBefore int
	StatusAfter  int
	Changes      []Change
}

// IsEmpty reports whether no differences were found at all.
func (r Result) IsEmpty() bool {
	return r.StatusBefore == r.StatusAfter && len(r.Changes) == 0
}

// Compare diffs two HTTP responses structurally. bodies are raw bytes;
// non-JSON bodies are compared as opaque byte-equal/not-equal at path "$".
func Compare(statusBefore, statusAfter int, bodyBefore, bodyAfter []byte) Result {
	res := Result{StatusBefore: statusBefore, StatusAfter: statusAfter}
	if statusBefore != statusAfter {
		res.Changes = append(res.Changes, Change{Path: "$.status", Type: StatusChanged, Before: statusBefore, After: statusAfter})
		if statusBefore < 500 && statusAfter >= 500 {
			res.Changes = append(res.Changes, Change{Path: "$.status", Type: ErrorAppeared, Before: statusBefore, After: statusAfter})
		}
	}

	before, okB := normalize.ParseJSON(bodyBefore)
	after, okA := normalize.ParseJSON(bodyAfter)
	if !okB || !okA {
		if string(bodyBefore) != string(bodyAfter) {
			res.Changes = append(res.Changes, Change{Path: "$", Type: ValueChanged, Before: string(bodyBefore), After: string(bodyAfter)})
		}
		return res
	}
	compareValues("$", before, after, &res.Changes)
	return res
}

func compareValues(path string, before, after any, changes *[]Change) {
	bm, bIsMap := before.(map[string]any)
	am, aIsMap := after.(map[string]any)
	if bIsMap && aIsMap {
		compareMaps(path, bm, am, changes)
		return
	}

	barr, bIsArr := before.([]any)
	aarr, aIsArr := after.([]any)
	if bIsArr && aIsArr {
		compareArrays(path, barr, aarr, changes)
		return
	}

	if typeName(before) != typeName(after) {
		*changes = append(*changes, Change{Path: path, Type: TypeChanged, Before: before, After: after})
		return
	}

	if !scalarEqual(before, after) {
		*changes = append(*changes, Change{Path: path, Type: ValueChanged, Before: before, After: after})
	}
}

func compareMaps(path string, before, after map[string]any, changes *[]Change) {
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	for _, k := range sorted {
		childPath := fmt.Sprintf("%s.%s", path, k)
		bv, bOK := before[k]
		av, aOK := after[k]
		switch {
		case bOK && !aOK:
			*changes = append(*changes, Change{Path: childPath, Type: FieldRemoved, Before: bv, After: nil})
		case !bOK && aOK:
			*changes = append(*changes, Change{Path: childPath, Type: FieldAdded, Before: nil, After: av})
		default:
			compareValues(childPath, bv, av, changes)
		}
	}
}

func compareArrays(path string, before, after []any, changes *[]Change) {
	keyField := normalize.KeyedArrayKeyField(before)
	if keyField != "" && keyField == normalize.KeyedArrayKeyField(after) {
		compareKeyedArrays(path, keyField, before, after, changes)
		return
	}

	if len(before) != len(after) {
		if isSameMultiset(before, after) {
			// Shouldn't happen (different lengths can't be same multiset),
			// kept defensive; falls through to length-changed below.
		}
		*changes = append(*changes, Change{Path: path, Type: ArrayLengthChanged, Before: len(before), After: len(after)})
		return
	}
	if !elementsEqualInOrder(before, after) {
		if isSameMultiset(before, after) {
			*changes = append(*changes, Change{Path: path, Type: ArrayOrderChanged, Before: before, After: after})
			return
		}
		for i := range before {
			compareValues(fmt.Sprintf("%s[%d]", path, i), before[i], after[i], changes)
		}
	}
}

func compareKeyedArrays(path, keyField string, before, after []any, changes *[]Change) {
	byKeyBefore := map[string]any{}
	byKeyAfter := map[string]any{}
	for _, el := range before {
		byKeyBefore[keyOf(el, keyField)] = el
	}
	for _, el := range after {
		byKeyAfter[keyOf(el, keyField)] = el
	}
	allKeys := map[string]bool{}
	for k := range byKeyBefore {
		allKeys[k] = true
	}
	for k := range byKeyAfter {
		allKeys[k] = true
	}
	sorted := make([]string, 0, len(allKeys))
	for k := range allKeys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		childPath := fmt.Sprintf("%s[%s=%s]", path, keyField, k)
		b, bOK := byKeyBefore[k]
		a, aOK := byKeyAfter[k]
		switch {
		case bOK && !aOK:
			*changes = append(*changes, Change{Path: childPath, Type: FieldRemoved, Before: b})
		case !bOK && aOK:
			*changes = append(*changes, Change{Path: childPath, Type: FieldAdded, After: a})
		default:
			compareValues(childPath, b, a, changes)
		}
	}
}

func keyOf(el any, keyField string) string {
	obj, _ := el.(map[string]any)
	v := obj[keyField]
	return scalarString(v)
}

func scalarString(v any) string { return fmt.Sprintf("%v", v) }

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "bool"
	default:
		return "number"
	}
}

func scalarEqual(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func elementsEqualInOrder(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if fmt.Sprintf("%v", a[i]) != fmt.Sprintf("%v", b[i]) {
			return false
		}
	}
	return true
}

func isSameMultiset(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, v := range a {
		counts[scalarString(v)]++
	}
	for _, v := range b {
		counts[scalarString(v)]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}
