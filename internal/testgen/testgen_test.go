package testgen

import (
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/jex2l/parity/internal/recording/pb"
)

func ex(id, pathTemplate, path string, status int32, body string) *pb.Exchange {
	return &pb.Exchange{
		ExchangeId: id,
		Inbound:    &pb.Inbound{Method: "GET", Path: path, PathTemplate: pathTemplate},
		Response:   &pb.Response{Status: status, Body: []byte(body)},
	}
}

func TestGenerate_DeduplicatesByEndpointAndStatus(t *testing.T) {
	exchanges := []*pb.Exchange{
		ex("e1", "/orders/{id}", "/orders/1", 200, `{"id":1}`),
		ex("e2", "/orders/{id}", "/orders/2", 200, `{"id":2}`), // same template+status as e1, should be deduped
		ex("e3", "/orders/{id}", "/orders/3", 404, `{"error":"not found"}`),
	}
	cases := Generate(exchanges, nil)
	if len(cases) != 2 {
		t.Fatalf("expected 2 cases (one per distinct endpoint+status), got %d: %+v", len(cases), cases)
	}
}

func TestGenerate_AlwaysIncludesMustInclude(t *testing.T) {
	exchanges := []*pb.Exchange{
		ex("e1", "/orders/{id}", "/orders/1", 200, `{"id":1}`),
		ex("e2", "/orders/{id}", "/orders/2", 200, `{"id":2}`),
	}
	cases := Generate(exchanges, map[string]bool{"e2": true})
	found := false
	for _, c := range cases {
		if c.Provenance == "exchange e2 (exposed a regression this run)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected e2 to be included as a must-include case even though it dedupes with e1, got %+v", cases)
	}
}

func TestMaskVolatile_UUIDAndTimestamp(t *testing.T) {
	in := `{"id":"a1b2c3d4-0000-0000-0000-000000000001","created_at":"2024-01-01T00:00:00Z","total":10}`
	out := MaskVolatile(in)
	if out == in {
		t.Fatalf("expected masking to change the body")
	}
	// Re-masking an already-masked body must be a no-op (idempotent).
	if MaskVolatile(out) != out {
		t.Fatalf("masking should be idempotent")
	}
}

func TestRun_PassesOnMatchingServerAndFailsOnMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orders/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":1,"total":9.5,"ts":"2024-01-01T00:00:00Z"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := Case{
		Method:         "GET",
		Path:           "/orders/1",
		ExpectedStatus: 200,
		ExpectedBody:   MaskVolatile(`{"id":1,"total":9.5,"ts":"2024-01-02T00:00:00Z"}`), // different timestamp, should still match
	}
	results := Run(srv.URL, []Case{c})
	if !results[0].Passed {
		t.Fatalf("expected pass (timestamp masked), got failure: %s", results[0].Reason)
	}

	bad := c
	bad.ExpectedStatus = 201
	results2 := Run(srv.URL, []Case{bad})
	if results2[0].Passed {
		t.Fatalf("expected failure on status mismatch")
	}
}
