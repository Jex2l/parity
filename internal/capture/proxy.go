// Package capture implements the Parity capture reverse proxy: it forwards
// traffic to the upstream service unmodified and asynchronously records
// sanitized exchanges, failing open (never blocking the request path) if
// recording falls behind or breaks.
package capture

import (
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	pb "github.com/jex2l/parity/internal/recording/pb"
)

// Sink receives raw (not-yet-scrubbed) exchanges off the hot path. The
// capture proxy never calls this synchronously with the client request;
// it is always invoked from the background drain goroutine.
type Sink interface {
	Accept(*pb.Exchange)
}

// Metrics exposes fail-open counters for observability (Prometheus
// wiring happens in cmd/parity-capture; this stays dependency-free).
type Metrics struct {
	Captured int64
	Dropped  int64
	Denied   int64
}

// Proxy is the capture reverse proxy.
type Proxy struct {
	upstream  *httputil.ReverseProxy
	templater *Templater
	sampler   Sampler
	denyPaths map[string]bool
	service   string
	buildSHA  string
	queue     chan *pb.Exchange
	Metrics   Metrics
}

// Options configures a new Proxy.
type Options struct {
	Upstream    *url.URL
	Templater   *Templater
	Sampler     Sampler
	DenyPaths   []string
	Service     string
	BuildSHA    string
	QueueDepth  int // bounded ring buffer size; 0 defaults to 4096
}

// NewProxy builds a capture Proxy forwarding to opts.Upstream.
func NewProxy(opts Options) *Proxy {
	depth := opts.QueueDepth
	if depth == 0 {
		depth = 4096
	}
	deny := map[string]bool{}
	for _, p := range opts.DenyPaths {
		deny[p] = true
	}
	templater := opts.Templater
	if templater == nil {
		templater = NewTemplater()
	}
	rp := httputil.NewSingleHostReverseProxy(opts.Upstream)
	p := &Proxy{
		upstream:  rp,
		templater: templater,
		sampler:   opts.Sampler,
		denyPaths: deny,
		service:   opts.Service,
		buildSHA:  opts.BuildSHA,
		queue:     make(chan *pb.Exchange, depth),
	}
	return p
}

// Run starts the background goroutine that drains the queue into sink.
// Call it once; it returns a stop function. Unlike an abrupt cancel, stop
// first drains whatever is still buffered in the queue into sink before
// returning (bounded by drainTimeout), so a clean shutdown doesn't throw
// away exchanges that were already successfully captured — fail-open is
// for the hot path staying fast under backpressure, not an excuse to
// drop data on an orderly stop.
func (p *Proxy) Run(sink Sink) (stop func()) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case ex, ok := <-p.queue:
				if !ok {
					return
				}
				sink.Accept(ex)
			case <-done:
				p.drainRemaining(sink)
				return
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

// drainRemaining flushes whatever is currently buffered in the queue,
// without blocking on new arrivals — called only during shutdown.
func (p *Proxy) drainRemaining(sink Sink) {
	for {
		select {
		case ex := <-p.queue:
			sink.Accept(ex)
		default:
			return
		}
	}
}

// ServeHTTP forwards the request to upstream immediately and, off the hot
// path, enqueues a captured exchange (subject to sampling and the bounded
// queue). If the queue is full the exchange is dropped and Metrics.Dropped
// is incremented — the client request is never slowed or blocked.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.denyPaths[r.URL.Path] {
		atomic.AddInt64(&p.Metrics.Denied, 1)
		p.upstream.ServeHTTP(w, r)
		return
	}

	// Tee the request body into a capped capture buffer while streaming
	// the full, untouched body to upstream. This matters: an earlier
	// version of this proxy pre-read the body through a LimitReader and
	// replaced r.Body with only what it read, which would silently
	// truncate any upload over the capture cap before it ever reached
	// the real service — a correctness bug, not just a capture-fidelity
	// one. A capped buffer never errors on Write (it just stops storing
	// past the cap), so io.TeeReader never short-circuits the read
	// upstream is doing.
	var reqCapture *cappedBuffer
	if r.Body != nil {
		reqCapture = newCappedBuffer(maxCapturedBodyBytes)
		r.Body = io.NopCloser(io.TeeReader(r.Body, reqCapture))
	}

	rec := &responseRecorder{ResponseWriter: w, buf: newCappedBuffer(maxCapturedBodyBytes), status: 200}
	start := time.Now()
	p.upstream.ServeHTTP(rec, r)
	duration := time.Since(start)

	// Everything below is off the synchronous client path already (the
	// response has been written); building and enqueuing the exchange
	// happens here but must never panic the handler goroutine.
	defer func() { _ = recover() }()

	template := p.templater.Template(r.URL.Path)
	isError := rec.status >= 500
	meta := SampleMeta{Status: rec.status, DurationMs: float64(duration.Microseconds()) / 1000, IsError: isError}

	var decision Decision
	if p.sampler != nil {
		decision = p.sampler.Decide(template, meta)
	} else {
		decision = Decision{Keep: true, Weight: 1}
	}
	if !decision.Keep {
		return
	}

	flags := []pb.FidelityFlag{}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		flags = append(flags, pb.FidelityFlag_HAS_WRITE_SIDE_EFFECTS)
	}
	var reqBody []byte
	if reqCapture != nil {
		reqBody = reqCapture.Bytes()
		if reqCapture.Truncated() {
			flags = append(flags, pb.FidelityFlag_LARGE_BODY_TRUNCATED)
		}
	}
	if rec.buf.Truncated() {
		flags = append(flags, pb.FidelityFlag_LARGE_BODY_TRUNCATED)
	}

	ex := &pb.Exchange{
		ExchangeId:           uuid.New().String(),
		TraceId:              r.Header.Get("X-Request-Id"),
		CapturedAtUnixMicros: start.UnixMicro(),
		Service:              p.service,
		BuildSha:             p.buildSHA,
		Inbound: &pb.Inbound{
			Method:          r.Method,
			Scheme:          schemeOf(r),
			Host:            r.Host,
			Path:            r.URL.Path,
			PathTemplate:    template,
			Query:           r.URL.RawQuery,
			Headers:         headersToPB(r.Header),
			Body:            reqBody,
			BodyContentType: r.Header.Get("Content-Type"),
			ProtocolVersion: r.Proto,
		},
		Response: &pb.Response{
			Status:               int32(rec.status),
			Headers:              headersToPB(rec.Header()),
			Body:                 rec.buf.Bytes(),
			TotalDurationMicros:  duration.Microseconds(),
		},
		Sampling: &pb.Sampling{
			Strategy: "composite",
			Weight:   decision.Weight,
		},
		FidelityFlags: flags,
	}

	select {
	case p.queue <- ex:
		atomic.AddInt64(&p.Metrics.Captured, 1)
	default:
		atomic.AddInt64(&p.Metrics.Dropped, 1) // fail open: never block
	}
}

func schemeOf(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func headersToPB(h http.Header) []*pb.Header {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]*pb.Header, 0, len(names))
	for _, k := range names {
		out = append(out, &pb.Header{Name: k, Values: h[k]})
	}
	return out
}

// responseRecorder captures the status and body written by the upstream
// handler while still writing the full, untouched response through to
// the real client — capping only matters for what gets captured, never
// for what the client receives.
type responseRecorder struct {
	http.ResponseWriter
	buf         *cappedBuffer
	status      int
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	r.buf.Write(b) // cappedBuffer.Write never errors and never blocks capture on size
	return r.ResponseWriter.Write(b)
}

// maxCapturedBodyBytes bounds how much of a request/response body is
// captured for recording. It never bounds what's actually forwarded
// between client and upstream — see ServeHTTP's use of io.TeeReader and
// cappedBuffer for why that distinction matters.
const maxCapturedBodyBytes = 1 << 20 // 1MB

// cappedBuffer accumulates up to a fixed number of bytes and silently
// (but trackably, via Truncated) drops anything past that — Write never
// returns an error, so wrapping it in io.TeeReader or calling it from a
// hot-path handler can never break the real data flow it's observing.
type cappedBuffer struct {
	cap       int
	buf       []byte
	truncated bool
}

func newCappedBuffer(capBytes int) *cappedBuffer {
	return &cappedBuffer{cap: capBytes}
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := c.cap - len(c.buf)
	if room <= 0 {
		if len(p) > 0 {
			c.truncated = true
		}
		return len(p), nil
	}
	if room >= len(p) {
		c.buf = append(c.buf, p...)
		return len(p), nil
	}
	c.buf = append(c.buf, p[:room]...)
	c.truncated = true
	return len(p), nil
}

func (c *cappedBuffer) Bytes() []byte { return c.buf }

// Truncated reports whether more was written than the cap could hold.
func (c *cappedBuffer) Truncated() bool { return c.truncated }
