package recording

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/quick"

	pb "github.com/jex2l/parity/internal/recording/pb"
)

func sampleExchange(i int) *pb.Exchange {
	return &pb.Exchange{
		ExchangeId:           "ex-" + string(rune('a'+i%26)),
		Service:              "orders-api",
		CapturedAtUnixMicros: int64(i) * 1000,
		Inbound: &pb.Inbound{
			Method:       "GET",
			Path:         "/orders/42",
			PathTemplate: "/orders/{id}",
		},
		Response: &pb.Response{
			Status: int32(200 + i%3),
		},
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seg-000001.zst")

	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	const n = 50
	for i := 0; i < n; i++ {
		if err := w.Write(sampleExchange(i)); err != nil {
			t.Fatalf("Write(%d): %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := NewReader(path)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	got := 0
	for {
		ex, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next(%d): %v", got, err)
		}
		want := sampleExchange(got)
		if ex.GetExchangeId() != want.GetExchangeId() || ex.GetResponse().GetStatus() != want.GetResponse().GetStatus() {
			t.Fatalf("record %d mismatch: got %+v want %+v", got, ex, want)
		}
		got++
	}
	if got != n {
		t.Fatalf("got %d records, want %d", got, n)
	}

	idx, err := ReadIndex(path)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if len(idx) != n {
		t.Fatalf("index has %d entries, want %d", len(idx), n)
	}
	if idx[0].EndpointTmpl != "/orders/{id}" {
		t.Fatalf("index entry 0 endpoint = %q", idx[0].EndpointTmpl)
	}
}

// TestTruncationRecovery ensures that truncating a segment file after some
// records were durably flushed does not lose those earlier records, and
// that Next() reports ErrTruncated rather than panicking or silently
// fabricating a record.
func TestTruncationRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seg-000001.zst")

	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	const n = 20
	for i := 0; i < n; i++ {
		if err := w.Write(sampleExchange(i)); err != nil {
			t.Fatalf("Write(%d): %v", i, err)
		}
	}
	// Do not call Close (which writes a zstd end-of-frame marker); simulate
	// a crash by abandoning the writer after the last flush, then truncate
	// the raw file to chop off trailing bytes.
	w.zw.Close() // release file handle without the writer's own bookkeeping
	w.f.Close()
	w.idxF.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	truncated := info.Size() - info.Size()/4 // chop off the last ~25%
	if err := os.Truncate(path, truncated); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	r, err := NewReader(path)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	recovered := 0
	for {
		_, err := r.Next()
		if err == io.EOF {
			t.Fatalf("expected ErrTruncated before EOF on a chopped segment")
		}
		if err != nil {
			break // ErrTruncated (or a wrapped decode error at the cut point)
		}
		recovered++
	}
	if recovered == 0 {
		t.Fatalf("expected at least some records recovered before truncation point")
	}
	if recovered >= n {
		t.Fatalf("expected fewer than %d records recovered after truncation, got %d", n, recovered)
	}
	t.Logf("recovered %d/%d records from truncated segment", recovered, n)
}

// TestQuickRoundTrip is a property-based test: for any sequence of random
// exchanges, writing then reading must reproduce the same exchange IDs in
// order.
func TestQuickRoundTrip(t *testing.T) {
	f := func(ids []string) bool {
		if len(ids) == 0 {
			return true
		}
		if len(ids) > 200 {
			ids = ids[:200]
		}
		dir, err := os.MkdirTemp("", "parity-quick-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "seg.zst")

		w, err := NewWriter(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if err := w.Write(&pb.Exchange{ExchangeId: id}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}

		r, err := NewReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		for _, want := range ids {
			ex, err := r.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if ex.GetExchangeId() != want {
				return false
			}
		}
		_, err = r.Next()
		return err == io.EOF
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 50}); err != nil {
		t.Error(err)
	}
}
