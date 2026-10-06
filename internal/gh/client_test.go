package gh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestSignAppJWT_ValidAndParseable(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tok, err := SignAppJWT("12345", key)
	if err != nil {
		t.Fatalf("SignAppJWT: %v", err)
	}

	parsed, err := jwt.Parse(tok, func(t *jwt.Token) (any, error) { return &key.PublicKey, nil })
	if err != nil {
		t.Fatalf("parse signed JWT: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["iss"] != "12345" {
		t.Fatalf("iss claim = %v, want 12345", claims["iss"])
	}
}

func TestCreateCheckRun_PostsExpectedPayload(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody CheckRun
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing/wrong Authorization header: %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 1})
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "test-token", HTTP: http.DefaultClient}
	err := c.CreateCheckRun(context.Background(), "acme", "widgets", CheckRun{
		Name: "Parity / behavioral diff", HeadSHA: "abc123", Status: "completed", Conclusion: ConclusionFailure,
		Output: CheckRunOutput{Title: "2 unintended diffs", Summary: "details..."},
	})
	if err != nil {
		t.Fatalf("CreateCheckRun: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/repos/acme/widgets/check-runs" {
		t.Fatalf("unexpected request: %s %s", gotMethod, gotPath)
	}
	if gotBody.Conclusion != ConclusionFailure || gotBody.HeadSHA != "abc123" {
		t.Fatalf("unexpected body: %+v", gotBody)
	}
}

func TestUpsertComment_CreatesWhenNoneExists(t *testing.T) {
	var postedBody Comment
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode([]Comment{}) // no existing comments
		case http.MethodPost:
			json.NewDecoder(r.Body).Decode(&postedBody)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(Comment{ID: 1, Body: postedBody.Body})
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: http.DefaultClient}
	if err := c.UpsertComment(context.Background(), "acme", "widgets", 42, "## Parity report\n\nclean"); err != nil {
		t.Fatalf("UpsertComment: %v", err)
	}
	if !hasMarker(postedBody.Body) {
		t.Fatalf("expected posted comment to carry the parity marker, got: %s", postedBody.Body)
	}
}

func TestUpsertComment_UpdatesExistingInstead(t *testing.T) {
	existing := Comment{ID: 99, Body: commentMarker + "\nold report"}
	var patchedID string
	var patchedBody Comment
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode([]Comment{existing})
		case http.MethodPatch:
			patchedID = r.URL.Path
			json.NewDecoder(r.Body).Decode(&patchedBody)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(Comment{})
		case http.MethodPost:
			t.Fatalf("should not POST a new comment when one already exists")
		}
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: http.DefaultClient}
	if err := c.UpsertComment(context.Background(), "acme", "widgets", 42, "new report"); err != nil {
		t.Fatalf("UpsertComment: %v", err)
	}
	if patchedID == "" {
		t.Fatalf("expected a PATCH to the existing comment, got none")
	}
	if !hasMarker(patchedBody.Body) {
		t.Fatalf("expected updated body to still carry the marker")
	}
}
