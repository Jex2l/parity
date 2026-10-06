package capture

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jex2l/parity/internal/recording/pb"
)

func startUpstream(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":123}`))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type countingSink struct{ n atomic.Int64 }

func (c *countingSink) Accept(*pb.Exchange) { c.n.Add(1) }

func TestProxy_ForwardsAndCaptures(t *testing.T) {
	upstream := startUpstream(t)
	u, _ := url.Parse(upstream.URL)

	p := NewProxy(Options{
		Upstream:  u,
		Templater: NewTemplater(),
		Sampler:   NewCompositeSampler(Config{DefaultRate: 1.0}),
		DenyPaths: []string{"/health"},
		Service:   "test-svc",
	})
	sink := &countingSink{}
	stop := p.Run(sink)
	defer stop()

	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := http.Get(front.URL + "/users/123")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// deny-listed path should still be forwarded but not captured
	resp2, _ := http.Get(front.URL + "/health")
	resp2.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for sink.n.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sink.n.Load() != 1 {
		t.Fatalf("captured %d exchanges, want exactly 1 (the non-denied path)", sink.n.Load())
	}
	if p.Metrics.Denied != 1 {
		t.Fatalf("denied count = %d, want 1", p.Metrics.Denied)
	}
}

// slowSink simulates a recorder that cannot keep up, to exercise fail-open.
type slowSink struct{ n atomic.Int64 }

func (s *slowSink) Accept(*pb.Exchange) {
	time.Sleep(50 * time.Millisecond)
	s.n.Add(1)
}

// TestProxy_FailsOpenUnderBackpressure verifies that when the recording
// pipeline is too slow to keep up, client requests are never blocked and
// the proxy degrades by dropping (and counting) exchanges rather than
// applying backpressure to the hot path.
func TestProxy_FailsOpenUnderBackpressure(t *testing.T) {
	upstream := startUpstream(t)
	u, _ := url.Parse(upstream.URL)

	p := NewProxy(Options{
		Upstream:   u,
		Templater:  NewTemplater(),
		Sampler:    NewCompositeSampler(Config{DefaultRate: 1.0}),
		Service:    "test-svc",
		QueueDepth: 4, // tiny queue to force drops quickly
	})
	sink := &slowSink{}
	stop := p.Run(sink)
	defer stop()

	front := httptest.NewServer(p)
	defer front.Close()

	const n = 200
	start := time.Now()
	for i := 0; i < n; i++ {
		resp, err := http.Get(front.URL + "/users/123")
		if err != nil {
			t.Fatalf("GET %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200 (client must never see capture failure)", i, resp.StatusCode)
		}
	}
	elapsed := time.Since(start)
	// With a 50ms-per-record slow sink and only 4 buffer slots, if the
	// proxy were blocking on capture this would take >= n*50ms = 10s.
	// Fail-open means total time stays close to native upstream latency.
	if elapsed > 2*time.Second {
		t.Fatalf("requests took %v; capture appears to be blocking the hot path (fail-open violated)", elapsed)
	}
	if atomic.LoadInt64(&p.Metrics.Dropped) == 0 {
		t.Fatalf("expected some drops under backpressure, got 0 (test setup may not be inducing backpressure)")
	}
	t.Logf("%d requests in %v, captured=%d dropped=%d", n, elapsed, p.Metrics.Captured, p.Metrics.Dropped)
}

// recordingSink captures every exchange it sees, for assertions.
type recordingSink struct {
	mu        sync.Mutex
	exchanges []*pb.Exchange
}

func (s *recordingSink) Accept(ex *pb.Exchange) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exchanges = append(s.exchanges, ex)
}

func (s *recordingSink) latest() *pb.Exchange {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.exchanges) == 0 {
		return nil
	}
	return s.exchanges[len(s.exchanges)-1]
}

// TestProxy_ForwardsFullBodyEvenWhenCaptureIsTruncated is the
// correctness regression test for the capped-buffer fix: a request body
// larger than the 1MB capture cap must still reach upstream in full. An
// earlier version of this proxy pre-read the body through a LimitReader
// and replaced r.Body with only what it read, silently corrupting any
// upload over the cap before it reached the real service.
func TestProxy_ForwardsFullBodyEvenWhenCaptureIsTruncated(t *testing.T) {
	bodySize := maxCapturedBodyBytes + 1024 // comfortably over the cap
	bigBody := bytes.Repeat([]byte("A"), bodySize)

	var receivedLen int
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedLen = len(b)
		w.WriteHeader(http.StatusOK)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	u, _ := url.Parse(upstream.URL)
	p := NewProxy(Options{
		Upstream:  u,
		Templater: NewTemplater(),
		Sampler:   NewCompositeSampler(Config{DefaultRate: 1.0}),
		Service:   "test-svc",
	})
	sink := &recordingSink{}
	stop := p.Run(sink)
	defer stop()

	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := http.Post(front.URL+"/upload", "application/octet-stream", bytes.NewReader(bigBody))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if receivedLen != bodySize {
		t.Fatalf("upstream received %d bytes, want the full %d (capture must never truncate what's forwarded)", receivedLen, bodySize)
	}

	deadline := time.Now().Add(2 * time.Second)
	for sink.latest() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ex := sink.latest()
	if ex == nil {
		t.Fatalf("expected the exchange to be captured")
	}
	if len(ex.GetInbound().GetBody()) != maxCapturedBodyBytes {
		t.Fatalf("captured body = %d bytes, want exactly the %d-byte cap", len(ex.GetInbound().GetBody()), maxCapturedBodyBytes)
	}
	foundFlag := false
	for _, f := range ex.GetFidelityFlags() {
		if f == pb.FidelityFlag_LARGE_BODY_TRUNCATED {
			foundFlag = true
		}
	}
	if !foundFlag {
		t.Fatalf("expected LARGE_BODY_TRUNCATED to be set on an exchange whose body exceeded the capture cap, flags=%v", ex.GetFidelityFlags())
	}
}

func TestTemplater(t *testing.T) {
	tpl := NewTemplater()
	cases := map[string]string{
		"/users/123/orders/9":               "/users/{id}/orders/{id}",
		"/users/550e8400-e29b-41d4-a716-446655440000": "/users/{id}",
		"/health":                            "/health",
	}
	for path, want := range cases {
		if got := tpl.Template(path); got != want {
			t.Errorf("Template(%q) = %q, want %q", path, got, want)
		}
	}
}
