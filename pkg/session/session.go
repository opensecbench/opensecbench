// Package session runs interactive terminal sessions through a sandboxed runner (ADR-0007). The
// control plane holds the PTY and bridges its bytes to a client (a WebSocket), teeing everything
// into a bounded transcript that is captured to the CAS on close for audit + evidence.
package session

import (
	"io"
	"sync"
)

// MaxTranscriptBytes caps a captured transcript; once reached, further output still streams to the
// client but is dropped from the transcript (with a marker) so a long session can't exhaust memory.
const MaxTranscriptBytes = 1 << 20 // 1 MiB

// PTY is a pseudo-terminal: a bidirectional byte stream that can be resized and closed.
type PTY interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
}

// Handle is a live session's terminal. It wraps a PTY, recording all output into a transcript, and
// owns teardown (closing the PTY and stopping the underlying container).
type Handle struct {
	Container string

	pty  PTY
	stop func()

	mu         sync.Mutex
	transcript []byte
	truncated  bool
	closed     bool
}

func newHandle(container string, pty PTY, stop func()) *Handle {
	if stop == nil {
		stop = func() {}
	}
	return &Handle{Container: container, pty: pty, stop: stop}
}

// Read returns terminal output, teeing it into the transcript.
func (h *Handle) Read(p []byte) (int, error) {
	n, err := h.pty.Read(p)
	if n > 0 {
		h.record(p[:n])
	}
	return n, err
}

// Write sends input (keystrokes) to the terminal.
func (h *Handle) Write(p []byte) (int, error) { return h.pty.Write(p) }

// Resize adjusts the terminal window size.
func (h *Handle) Resize(rows, cols uint16) error { return h.pty.Resize(rows, cols) }

func (h *Handle) record(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := MaxTranscriptBytes - len(h.transcript)
	if room <= 0 {
		h.truncated = true
		return
	}
	if len(b) > room {
		b = b[:room]
		h.truncated = true
	}
	h.transcript = append(h.transcript, b...)
}

// Transcript returns a copy of everything captured so far.
func (h *Handle) Transcript() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]byte, len(h.transcript))
	copy(out, h.transcript)
	if h.truncated {
		out = append(out, []byte("\n[transcript truncated]\n")...)
	}
	return out
}

// Close tears the session down: it closes the PTY and stops the container. It is idempotent.
func (h *Handle) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.mu.Unlock()

	err := h.pty.Close()
	h.stop()
	return err
}
