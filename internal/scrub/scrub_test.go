package scrub

import (
	"strings"
	"testing"

	pb "github.com/jex2l/parity/internal/recording/pb"
)

func TestScrubHeaders_StripsAuthRegardlessOfAllowlist(t *testing.T) {
	s := New(Config{HeadersAllow: []string{"authorization", "content-type"}})
	ex := &pb.Exchange{
		Inbound: &pb.Inbound{
			Headers: []*pb.Header{
				{Name: "Authorization", Values: []string{"Bearer secrettoken"}},
				{Name: "Content-Type", Values: []string{"application/json"}},
				{Name: "X-Custom", Values: []string{"nope"}},
			},
		},
	}
	out, report := s.ScrubExchange(ex)
	for _, h := range out.GetInbound().GetHeaders() {
		if strings.EqualFold(h.GetName(), "Authorization") {
			t.Fatalf("Authorization header must always be stripped, got %+v", h)
		}
		if strings.EqualFold(h.GetName(), "X-Custom") {
			t.Fatalf("non-allowlisted header leaked: %+v", h)
		}
	}
	found := false
	for _, h := range out.GetInbound().GetHeaders() {
		if strings.EqualFold(h.GetName(), "Content-Type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("allowlisted header Content-Type was dropped")
	}
	if len(report.Redactions) == 0 {
		t.Fatalf("expected redactions to be recorded")
	}
}

// piiCorpus is a small synthetic corpus of PII-laden payloads used to
// measure scrub recall, per the eval harness target of >=99%.
var piiCorpus = []string{
	`{"email":"jane.doe@example.com"}`,
	`{"note":"call me at 415-555-0199 or (415) 555-0199"}`,
	`{"ssn":"123-45-6789"}`,
	`{"card":"4111 1111 1111 1111"}`,
	`{"card":"4012888888881881"}`,
	`{"token":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dGhpc2lzYXNpZ25hdHVyZQ"}`,
	`{"key":"sk_live_abcdefghijklmnop1234"}`,
	`{"ip":"192.168.1.100"}`,
	`{"iban":"GB29NWBK60161331926819"}`,
}

func TestScrubRecall_PIICorpus(t *testing.T) {
	s := New(Config{})
	detected := 0
	for _, payload := range piiCorpus {
		ex := &pb.Exchange{
			Inbound: &pb.Inbound{Body: []byte(payload), BodyContentType: "application/json"},
		}
		out, report := s.ScrubExchange(ex)
		if len(report.Redactions) > 0 && string(out.GetInbound().GetBody()) != payload {
			detected++
		} else {
			t.Logf("MISS on payload: %s", payload)
		}
	}
	recall := float64(detected) / float64(len(piiCorpus))
	if recall < 0.99 {
		t.Fatalf("scrub recall on PII corpus = %.2f, want >= 0.99 (%d/%d detected)", recall, detected, len(piiCorpus))
	}
	t.Logf("scrub recall on PII corpus: %.0f%% (%d/%d)", recall*100, detected, len(piiCorpus))
}

func TestPseudonymize_ReferentiallyConsistent(t *testing.T) {
	s := New(Config{})
	a := s.pseudonymize("email", "same@example.com")
	b := s.pseudonymize("email", "same@example.com")
	c := s.pseudonymize("email", "different@example.com")
	if a != b {
		t.Fatalf("pseudonymization not deterministic: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("different inputs collided: %q", a)
	}
}

func TestIsLuhnValid(t *testing.T) {
	cases := map[string]bool{
		"4111111111111111": true,
		"4111111111111112": false,
		"4012888888881881": true,
	}
	for num, want := range cases {
		if got := isLuhnValid(num); got != want {
			t.Errorf("isLuhnValid(%q) = %v, want %v", num, got, want)
		}
	}
}

func TestLooksLikeSecret(t *testing.T) {
	if LooksLikeSecret("short") {
		t.Fatalf("short string should not look like a secret")
	}
	if !LooksLikeSecret("aK9f!xQ2mP7zR4vL8nW1cT6bY3hJ0sD5") {
		t.Fatalf("high-entropy 32-char string should look like a secret")
	}
}
