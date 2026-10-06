package capture

import (
	"bytes"
	"io"
	"testing"
)

func TestCappedBuffer_UnderCapNotTruncated(t *testing.T) {
	c := newCappedBuffer(10)
	n, err := c.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if c.Truncated() {
		t.Fatalf("5 bytes into a 10-byte cap must not be truncated")
	}
	if string(c.Bytes()) != "hello" {
		t.Fatalf("Bytes() = %q", c.Bytes())
	}
}

func TestCappedBuffer_OverCapTruncatesButNeverErrors(t *testing.T) {
	c := newCappedBuffer(5)
	n, err := c.Write([]byte("hello world"))
	if err != nil {
		t.Fatalf("cappedBuffer.Write must never return an error, got %v", err)
	}
	if n != len("hello world") {
		t.Fatalf("Write must report the full length written (io.Writer contract), got %d", n)
	}
	if !c.Truncated() {
		t.Fatalf("expected Truncated() after writing past the cap")
	}
	if string(c.Bytes()) != "hello" {
		t.Fatalf("Bytes() = %q, want the first 5 bytes only", c.Bytes())
	}
}

func TestCappedBuffer_WritesAfterCapStayHonestAboutTruncation(t *testing.T) {
	c := newCappedBuffer(5)
	c.Write([]byte("hello"))
	if c.Truncated() {
		t.Fatalf("exactly at the cap with no extra bytes must not be marked truncated")
	}
	c.Write([]byte("more"))
	if !c.Truncated() {
		t.Fatalf("any write after the cap is already full must be marked truncated")
	}
	if string(c.Bytes()) != "hello" {
		t.Fatalf("Bytes() = %q, must not grow past the cap", c.Bytes())
	}
}

// TestCappedBuffer_AsTeeReaderTargetNeverBreaksTheStream proves the fix:
// io.TeeReader stops reading the moment its writer returns an error, so
// if cappedBuffer ever errored on overflow, a request/response body
// larger than the capture cap would get silently truncated on the real
// wire — exactly the correctness bug this type exists to avoid. This is
// the same construction ServeHTTP uses in proxy.go.
func TestCappedBuffer_AsTeeReaderTargetNeverBreaksTheStream(t *testing.T) {
	capBuf := newCappedBuffer(5)
	src := bytes.NewReader([]byte("this is a much longer body than the cap"))
	tee := io.TeeReader(src, capBuf)

	full, err := io.ReadAll(tee)
	if err != nil {
		t.Fatalf("reading through the tee must not fail just because the capture side is full: %v", err)
	}
	if string(full) != "this is a much longer body than the cap" {
		t.Fatalf("the full stream must still reach the real reader side, got %q", full)
	}
	if !capBuf.Truncated() {
		t.Fatalf("expected the capture side to report truncation")
	}
	if string(capBuf.Bytes()) != "this " {
		t.Fatalf("captured bytes = %q, want only the first 5", capBuf.Bytes())
	}
}
