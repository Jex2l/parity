package diff

import "testing"

func TestCompare_IdenticalIsEmpty(t *testing.T) {
	body := []byte(`{"id":1,"name":"a","tags":["x","y"]}`)
	r := Compare(200, 200, body, body)
	if !r.IsEmpty() {
		t.Fatalf("diff(a,a) should be empty, got %+v", r.Changes)
	}
}

func TestCompare_FieldAddedRemoved(t *testing.T) {
	before := []byte(`{"id":1,"old_field":"x"}`)
	after := []byte(`{"id":1,"new_field":"y"}`)
	r := Compare(200, 200, before, after)
	var sawAdded, sawRemoved bool
	for _, c := range r.Changes {
		if c.Type == FieldAdded && c.Path == "$.new_field" {
			sawAdded = true
		}
		if c.Type == FieldRemoved && c.Path == "$.old_field" {
			sawRemoved = true
		}
	}
	if !sawAdded || !sawRemoved {
		t.Fatalf("expected FIELD_ADDED and FIELD_REMOVED, got %+v", r.Changes)
	}
}

func TestCompare_TypeChanged(t *testing.T) {
	before := []byte(`{"price":9.99}`)
	after := []byte(`{"price":"9.99"}`)
	r := Compare(200, 200, before, after)
	if len(r.Changes) != 1 || r.Changes[0].Type != TypeChanged {
		t.Fatalf("expected one TYPE_CHANGED, got %+v", r.Changes)
	}
}

func TestCompare_StatusChanged5xx(t *testing.T) {
	r := Compare(200, 500, []byte(`{}`), []byte(`{"error":"boom"}`))
	foundError := false
	for _, c := range r.Changes {
		if c.Type == ErrorAppeared {
			foundError = true
		}
	}
	if !foundError {
		t.Fatalf("expected ERROR_APPEARED when status goes 2xx -> 5xx, got %+v", r.Changes)
	}
	if Score(Change{Type: ErrorAppeared}) != SeverityCritical {
		t.Fatalf("ERROR_APPEARED must be critical severity")
	}
}

func TestCompare_ArrayOrderVsLength(t *testing.T) {
	r1 := Compare(200, 200, []byte(`{"items":[1,2,3]}`), []byte(`{"items":[3,2,1]}`))
	if len(r1.Changes) != 1 || r1.Changes[0].Type != ArrayOrderChanged {
		t.Fatalf("expected ARRAY_ORDER_CHANGED for a permutation, got %+v", r1.Changes)
	}

	r2 := Compare(200, 200, []byte(`{"items":[1,2,3]}`), []byte(`{"items":[1,2]}`))
	if len(r2.Changes) != 1 || r2.Changes[0].Type != ArrayLengthChanged {
		t.Fatalf("expected ARRAY_LENGTH_CHANGED, got %+v", r2.Changes)
	}
}

func TestCompare_KeyedArrayDiffsByID(t *testing.T) {
	before := []byte(`{"items":[{"id":1,"status":"pending"},{"id":2,"status":"paid"}]}`)
	after := []byte(`{"items":[{"id":2,"status":"paid"},{"id":1,"status":"shipped"}]}`)
	r := Compare(200, 200, before, after)
	if len(r.Changes) != 1 {
		t.Fatalf("expected exactly one change (id=1 status), got %+v", r.Changes)
	}
	if r.Changes[0].Path != "$.items[id=1].status" {
		t.Fatalf("expected path $.items[id=1].status, got %q", r.Changes[0].Path)
	}
}

func TestNoiseModel_AbsorbsTimestampAndOpaqueID(t *testing.T) {
	abPairs := []Result{
		Compare(200, 200,
			[]byte(`{"request_id":"a1b2c3d4-0000-0000-0000-000000000001","generated_at":"2024-01-01T00:00:00Z","total":10}`),
			[]byte(`{"request_id":"a1b2c3d4-0000-0000-0000-000000000002","generated_at":"2024-01-01T00:00:05Z","total":10}`)),
	}
	model := Learn(abPairs)

	candidate := Compare(200, 200,
		[]byte(`{"request_id":"a1b2c3d4-0000-0000-0000-000000000003","generated_at":"2024-01-01T00:01:00Z","total":10}`),
		[]byte(`{"request_id":"a1b2c3d4-0000-0000-0000-000000000004","generated_at":"2024-01-01T00:02:00Z","total":11}`))

	signal, absorbed := Filter(candidate.Changes, model)
	if len(absorbed) != 2 {
		t.Fatalf("expected request_id and generated_at absorbed as noise, got absorbed=%+v signal=%+v", absorbed, signal)
	}
	if len(signal) != 1 || signal[0].Path != "$.total" {
		t.Fatalf("expected total change to be real signal, got %+v", signal)
	}
}

func TestClusters_GroupByEndpointAndPaths(t *testing.T) {
	diffs := []ExchangeDiff{
		{ExchangeID: "e1", Endpoint: "/orders/{id}", Signal: []Change{{Path: "$.total", Type: ValueChanged}}, Weight: 1},
		{ExchangeID: "e2", Endpoint: "/orders/{id}", Signal: []Change{{Path: "$.total", Type: ValueChanged}}, Weight: 1},
		{ExchangeID: "e3", Endpoint: "/orders/{id}", Signal: []Change{{Path: "$.status", Type: FieldRemoved}}, Weight: 1},
		{ExchangeID: "e4", Endpoint: "/orders/{id}", Signal: nil}, // absorbed entirely, not clustered
	}
	clusters := BuildClusters(diffs)
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d: %+v", len(clusters), clusters)
	}
	// FIELD_REMOVED (high) should sort before VALUE_CHANGED (medium).
	if clusters[0].Severity < clusters[1].Severity {
		t.Fatalf("expected clusters sorted by descending severity, got %+v", clusters)
	}
	totalCount := 0
	for _, c := range clusters {
		totalCount += c.Count
	}
	if totalCount != 3 {
		t.Fatalf("expected 3 total clustered exchanges (e4 excluded), got %d", totalCount)
	}
}

func TestFidelity_LowConfidenceReasoning(t *testing.T) {
	results := make([]ExchangeFidelity, 100)
	for i := range results {
		if i < 70 {
			results[i] = ExchangeFidelity{Reproduced: false, AuthFailed: true}
		} else {
			results[i] = ExchangeFidelity{Reproduced: true}
		}
	}
	f := Compute(results)
	if !f.IsLowConfidence() {
		t.Fatalf("expected low confidence at score %v", f.Score)
	}
	if f.Reason() == "" {
		t.Fatalf("expected a non-empty reason for low fidelity")
	}
	t.Logf("fidelity=%.2f reason=%q", f.Score, f.Reason())
}
