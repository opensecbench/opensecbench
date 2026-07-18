package session

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// fakePTY is an in-memory PTY: reads drain a preset output stream, writes are captured.
type fakePTY struct {
	out    *bytes.Reader
	in     bytes.Buffer
	closed bool
	resize [2]uint16
}

func (f *fakePTY) Read(p []byte) (int, error)  { return f.out.Read(p) }
func (f *fakePTY) Write(p []byte) (int, error) { return f.in.Write(p) }
func (f *fakePTY) Resize(rows, cols uint16) error {
	f.resize = [2]uint16{rows, cols}
	return nil
}
func (f *fakePTY) Close() error { f.closed = true; return nil }

func TestHandleTeesTranscriptAndTearsDown(t *testing.T) {
	fp := &fakePTY{out: bytes.NewReader([]byte("$ whoami\nroot\n"))}
	stopped := false
	h := newHandle("osb-sess-x", fp, func() { stopped = true })

	// Draining output feeds the transcript.
	got, err := io.ReadAll(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "$ whoami\nroot\n" {
		t.Fatalf("read = %q", got)
	}
	if string(h.Transcript()) != "$ whoami\nroot\n" {
		t.Fatalf("transcript = %q", h.Transcript())
	}

	// Input passes through to the PTY.
	if _, err := h.Write([]byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	if fp.in.String() != "exit\n" {
		t.Fatalf("pty input = %q", fp.in.String())
	}

	if err := h.Resize(40, 120); err != nil {
		t.Fatal(err)
	}
	if fp.resize != [2]uint16{40, 120} {
		t.Fatalf("resize = %v", fp.resize)
	}

	// Close is idempotent and tears down the container.
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if !fp.closed || !stopped {
		t.Fatalf("teardown incomplete: ptyClosed=%v stopped=%v", fp.closed, stopped)
	}
}

func TestTranscriptIsBounded(t *testing.T) {
	big := strings.Repeat("x", MaxTranscriptBytes+5000)
	h := newHandle("s", &fakePTY{out: bytes.NewReader([]byte(big))}, nil)
	if _, err := io.ReadAll(h); err != nil {
		t.Fatal(err)
	}
	tr := h.Transcript()
	if len(tr) > MaxTranscriptBytes+len("\n[transcript truncated]\n") {
		t.Fatalf("transcript not bounded: %d bytes", len(tr))
	}
	if !strings.Contains(string(tr), "[transcript truncated]") {
		t.Fatal("expected truncation marker")
	}
}

func TestOpenRealContainer(t *testing.T) {
	if !Available() {
		t.Skip("docker not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	h, err := NewManager("").Open(ctx, "osb-sess-test-open")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()

	// Drive a command and read until we see its output.
	if _, err := h.Write([]byte("echo hello-osb\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	buf := make([]byte, 4096)
	var seen strings.Builder
	for time.Now().Before(deadline) {
		_ = h.pty.(*osPTY).f.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := h.Read(buf)
		if n > 0 {
			seen.Write(buf[:n])
			if strings.Contains(seen.String(), "hello-osb") {
				break
			}
		}
	}
	if !strings.Contains(seen.String(), "hello-osb") {
		t.Fatalf("did not see command output; got %q", seen.String())
	}
	if !strings.Contains(string(h.Transcript()), "hello-osb") {
		t.Fatal("transcript missing command output")
	}
}
