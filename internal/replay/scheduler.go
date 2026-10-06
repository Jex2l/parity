package replay

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	pb "github.com/jex2l/parity/internal/recording/pb"
)

// ExchangeResult is what one recorded exchange produced when replayed
// against an Endpoint.
type ExchangeResult struct {
	ExchangeID string
	Endpoint   string // path template
	Status     int
	Body       []byte
	Duration   time.Duration
	Err        error
}

// Scheduler replays a fixed list of recorded exchanges against a target
// Endpoint at bounded concurrency.
type Scheduler struct {
	Concurrency int
	Client      *http.Client
}

// NewScheduler creates a Scheduler with sane defaults.
func NewScheduler(concurrency int) *Scheduler {
	if concurrency <= 0 {
		concurrency = 8
	}
	return &Scheduler{Concurrency: concurrency, Client: &http.Client{Timeout: 10 * time.Second}}
}

// Replay sends every inbound request in exchanges to target.BaseURL,
// preserving each exchange's method/path/query/body, and returns one
// ExchangeResult per input exchange in the same order.
func (s *Scheduler) Replay(ctx context.Context, target Endpoint, exchanges []*pb.Exchange) []ExchangeResult {
	results := make([]ExchangeResult, len(exchanges))
	sem := make(chan struct{}, s.Concurrency)
	var wg sync.WaitGroup

	for i, ex := range exchanges {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, ex *pb.Exchange) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = s.replayOne(ctx, target, ex)
		}(i, ex)
	}
	wg.Wait()
	return results
}

func (s *Scheduler) replayOne(ctx context.Context, target Endpoint, ex *pb.Exchange) ExchangeResult {
	in := ex.GetInbound()
	url := target.BaseURL + in.GetPath()
	if q := in.GetQuery(); q != "" {
		url += "?" + q
	}

	var bodyReader io.Reader
	if len(in.GetBody()) > 0 {
		bodyReader = bytes.NewReader(in.GetBody())
	}
	req, err := http.NewRequestWithContext(ctx, in.GetMethod(), url, bodyReader)
	if err != nil {
		return ExchangeResult{ExchangeID: ex.GetExchangeId(), Endpoint: in.GetPathTemplate(), Err: err}
	}
	if ct := in.GetBodyContentType(); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	start := time.Now()
	resp, err := s.Client.Do(req)
	duration := time.Since(start)
	if err != nil {
		return ExchangeResult{ExchangeID: ex.GetExchangeId(), Endpoint: in.GetPathTemplate(), Duration: duration, Err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	return ExchangeResult{
		ExchangeID: ex.GetExchangeId(),
		Endpoint:   in.GetPathTemplate(),
		Status:     resp.StatusCode,
		Body:       body,
		Duration:   duration,
	}
}
