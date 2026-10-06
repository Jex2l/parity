package capture

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/jex2l/parity/internal/recording"
	pb "github.com/jex2l/parity/internal/recording/pb"
	"github.com/jex2l/parity/internal/scrub"
)

// ScrubbingSink is a Sink that scrubs each exchange and appends it to
// rotating segment files under dir. Rotation is size-based (per the
// ~64-256MB segment guidance); defaults to 64MB for v1.
type ScrubbingSink struct {
	dir         string
	scrubber    *scrub.Scrubber
	maxBytes    int64

	mu      sync.Mutex
	writer  *recording.Writer
	path    string
	written int64
	seg     int
	log     *slog.Logger
}

// NewScrubbingSink creates a sink writing scrubbed exchanges into dir.
func NewScrubbingSink(dir string, scrubber *scrub.Scrubber, maxBytes int64) (*ScrubbingSink, error) {
	if maxBytes == 0 {
		maxBytes = 64 << 20
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", dir, err)
	}
	s := &ScrubbingSink{dir: dir, scrubber: scrubber, maxBytes: maxBytes, log: slog.Default()}
	if err := s.rotate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ScrubbingSink) rotate() error {
	if s.writer != nil {
		if err := s.writer.Close(); err != nil {
			return err
		}
	}
	s.seg++
	s.path = filepath.Join(s.dir, fmt.Sprintf("seg-%06d.zst", s.seg))
	w, err := recording.NewWriter(s.path)
	if err != nil {
		return fmt.Errorf("sink: new writer: %w", err)
	}
	s.writer = w
	s.written = 0
	return nil
}

// Accept implements Sink. It is always called from the capture proxy's
// single drain goroutine, so no per-call locking is strictly required, but
// the mutex is kept for safety if Close is called concurrently.
func (s *ScrubbingSink) Accept(ex *pb.Exchange) {
	s.mu.Lock()
	defer s.mu.Unlock()

	scrubbed, report := s.scrubber.ScrubExchange(ex)
	if err := s.writer.Write(scrubbed); err != nil {
		s.log.Error("sink: write failed, dropping record", "err", err)
		return
	}
	s.written += int64(len(scrubbed.String())) // rough size accounting for rotation
	if len(report.Redactions) > 0 {
		s.log.Debug("scrubbed exchange", "exchange_id", ex.GetExchangeId(), "redactions", len(report.Redactions))
	}
	if s.written >= s.maxBytes {
		if err := s.rotate(); err != nil {
			s.log.Error("sink: rotate failed", "err", err)
		}
	}
}

// Close flushes and closes the current segment.
func (s *ScrubbingSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writer == nil {
		return nil
	}
	return s.writer.Close()
}
