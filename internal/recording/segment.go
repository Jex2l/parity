// Package recording implements the Parity exchange recording format:
// length-delimited Exchange protobuf records, zstd-compressed, written to
// append-only segment files with a sidecar index for filtering.
package recording

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"

	pb "github.com/jex2l/parity/internal/recording/pb"
)

// ErrTruncated is returned by Reader.Next when the segment ends mid-record.
// Records read before this point are valid; this is not a fatal error for
// the caller, but it must be surfaced rather than silently swallowed.
var ErrTruncated = errors.New("recording: segment truncated mid-record")

// IndexEntry is one line of a segment's sidecar `.idx` file, used to filter
// which records to decode without materializing the whole segment.
type IndexEntry struct {
	Seq            int    `json:"seq"`
	ExchangeID     string `json:"exchange_id"`
	EndpointTmpl   string `json:"endpoint_template"`
	Status         int32  `json:"status"`
	CapturedAtUnix int64  `json:"captured_at_unix_micros"`
}

// Writer appends Exchange records to a segment file and its sidecar index.
type Writer struct {
	f        *os.File
	idxF     *os.File
	zw       *zstd.Encoder
	idxEnc   *json.Encoder
	seq      int
	closed   bool
}

// NewWriter creates (or truncates) the segment file at path and its
// sidecar index at path+".idx".
func NewWriter(path string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("recording: create segment: %w", err)
	}
	idxF, err := os.Create(path + ".idx")
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("recording: create index: %w", err)
	}
	zw, err := zstd.NewWriter(f)
	if err != nil {
		f.Close()
		idxF.Close()
		return nil, fmt.Errorf("recording: new zstd writer: %w", err)
	}
	return &Writer{f: f, idxF: idxF, zw: zw, idxEnc: json.NewEncoder(idxF)}, nil
}

// Write appends one Exchange record and its index entry, then flushes the
// compressor so a crash after Write leaves prior records recoverable.
func (w *Writer) Write(ex *pb.Exchange) error {
	if w.closed {
		return errors.New("recording: write on closed writer")
	}
	b, err := proto.Marshal(ex)
	if err != nil {
		return fmt.Errorf("recording: marshal exchange: %w", err)
	}
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(b)))
	if _, err := w.zw.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("recording: write length prefix: %w", err)
	}
	if _, err := w.zw.Write(b); err != nil {
		return fmt.Errorf("recording: write record: %w", err)
	}
	// Flush (not Close) so the stream stays open for more records but
	// everything so far is durably recoverable by a reader.
	if err := w.zw.Flush(); err != nil {
		return fmt.Errorf("recording: flush: %w", err)
	}

	entry := IndexEntry{
		Seq:            w.seq,
		ExchangeID:     ex.GetExchangeId(),
		EndpointTmpl:   ex.GetInbound().GetPathTemplate(),
		Status:         ex.GetResponse().GetStatus(),
		CapturedAtUnix: ex.GetCapturedAtUnixMicros(),
	}
	w.seq++
	if err := w.idxEnc.Encode(entry); err != nil {
		return fmt.Errorf("recording: write index entry: %w", err)
	}
	return nil
}

// Close flushes and closes the segment and its index.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	err1 := w.zw.Close()
	err2 := w.f.Close()
	err3 := w.idxF.Close()
	return errors.Join(err1, err2, err3)
}

// Reader streams Exchange records back out of a segment file.
type Reader struct {
	f  *os.File
	zr *zstd.Decoder
	br *bufio.Reader
}

// NewReader opens the segment file at path for streaming iteration.
func NewReader(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("recording: open segment: %w", err)
	}
	zr, err := zstd.NewReader(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("recording: new zstd reader: %w", err)
	}
	return &Reader{f: f, zr: zr, br: bufio.NewReaderSize(zr, 64*1024)}, nil
}

// Next returns the next Exchange, io.EOF at a clean end, or ErrTruncated
// (wrapping the underlying cause) if the segment ends mid-record. Records
// already returned before ErrTruncated remain valid.
func (r *Reader) Next() (*pb.Exchange, error) {
	var lenBuf [4]byte
	n, err := io.ReadFull(r.br, lenBuf[:])
	if err == io.EOF && n == 0 {
		return nil, io.EOF
	}
	if err != nil {
		return nil, fmt.Errorf("%w: reading length prefix: %v", ErrTruncated, err)
	}
	length := binary.LittleEndian.Uint32(lenBuf[:])
	buf := make([]byte, length)
	if _, err := io.ReadFull(r.br, buf); err != nil {
		return nil, fmt.Errorf("%w: reading record body: %v", ErrTruncated, err)
	}
	ex := &pb.Exchange{}
	if err := proto.Unmarshal(buf, ex); err != nil {
		return nil, fmt.Errorf("recording: unmarshal: %w", err)
	}
	return ex, nil
}

// Close releases the reader's resources.
func (r *Reader) Close() error {
	r.zr.Close()
	return r.f.Close()
}

// ReadIndex loads a segment's sidecar index file.
func ReadIndex(segmentPath string) ([]IndexEntry, error) {
	f, err := os.Open(segmentPath + ".idx")
	if err != nil {
		return nil, fmt.Errorf("recording: open index: %w", err)
	}
	defer f.Close()
	var entries []IndexEntry
	dec := json.NewDecoder(f)
	for dec.More() {
		var e IndexEntry
		if err := dec.Decode(&e); err != nil {
			return entries, fmt.Errorf("recording: decode index entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ExportJSONL writes every record in the segment at srcPath to dstPath as
// newline-delimited JSON (protojson), for debugging/inspection.
func ExportJSONL(srcPath, dstPath string) (int, error) {
	r, err := NewReader(srcPath)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	out, err := os.Create(dstPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	count := 0
	for {
		ex, err := r.Next()
		if err == io.EOF {
			break
		}
		if errors.Is(err, ErrTruncated) {
			break
		}
		if err != nil {
			return count, err
		}
		b, err := marshalJSON(ex)
		if err != nil {
			return count, err
		}
		if _, err := out.Write(append(b, '\n')); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
