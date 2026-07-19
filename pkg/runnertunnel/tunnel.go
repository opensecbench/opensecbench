// Package runnertunnel is a minimal multiplexed, flow-controlled stream protocol over a single
// message-oriented connection (a WebSocket in production). It lets the control plane open many concurrent
// logical streams to a remote runner over one outbound-connect socket, so proxied requests can be
// forwarded through the runner's network vantage with streaming response bodies (ADR-0026).
//
// Framing: each message is one frame — [streamID uint32 big-endian][type uint8][payload]. Flow control is
// credit-based: each direction of a stream starts with initialWindow bytes of credit, and the receiver
// grants more (WINDOW frames) as it consumes, so the shared reader always drains the socket into bounded
// per-stream buffers (no head-of-line blocking, no unbounded buffering). It is deliberately small — not a
// hardened yamux; one session per runner.
package runnertunnel

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

// binaryMessage mirrors websocket.BinaryMessage without importing gorilla here (the transport is injected).
const binaryMessage = 2

// Frame types.
const (
	frameOpen   = 1 // open a stream; payload = caller metadata
	frameData   = 2 // stream body bytes
	frameWindow = 3 // grant the peer more send credit; payload = uint32 bytes
	frameEOF    = 4 // half-close: no more DATA in this direction
	frameReset  = 5 // abort a stream; payload = error message
)

const (
	maxFrame      = 32 * 1024  // DATA chunk size
	initialWindow = 256 * 1024 // per-direction starting credit
)

// ErrSessionClosed is returned once the underlying connection has gone.
var ErrSessionClosed = errors.New("runnertunnel: session closed")

// MessageConn is the message-oriented transport the session runs over. *websocket.Conn satisfies it.
type MessageConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	Close() error
}

// Session multiplexes streams over one MessageConn. The initiator (control plane) opens streams; the
// acceptor (runner) receives them via Accept.
type Session struct {
	conn      MessageConn
	initiator bool

	writeMu sync.Mutex // serializes conn.WriteMessage (gorilla requires a single writer)

	mu       sync.Mutex
	streams  map[uint32]*Stream
	nextID   uint32
	acceptCh chan *Stream
	closed   chan struct{}
	closeErr error
	once     sync.Once
}

// New starts a session over conn. initiator=true for the side that opens streams (the control plane).
func New(conn MessageConn, initiator bool) *Session {
	s := &Session{
		conn:      conn,
		initiator: initiator,
		streams:   map[uint32]*Stream{},
		nextID:    1,
		acceptCh:  make(chan *Stream, 16),
		closed:    make(chan struct{}),
	}
	go s.readLoop()
	return s
}

// Open creates a new stream carrying meta to the peer (initiator only).
func (s *Session) Open(meta []byte) (*Stream, error) {
	s.mu.Lock()
	if s.closeErr != nil {
		s.mu.Unlock()
		return nil, s.closeErr
	}
	id := s.nextID
	s.nextID += 2 // initiator uses odd ids; acceptor never opens, so no collision either way
	st := newStream(s, id, meta)
	s.streams[id] = st
	s.mu.Unlock()

	if err := s.writeFrame(id, frameOpen, meta); err != nil {
		s.removeStream(id)
		return nil, err
	}
	return st, nil
}

// Accept returns the next stream opened by the peer (acceptor side). It blocks until one arrives or the
// session closes.
func (s *Session) Accept() (*Stream, error) {
	select {
	case st := <-s.acceptCh:
		return st, nil
	case <-s.closed:
		return nil, s.err()
	}
}

// Close tears down the session and all its streams.
func (s *Session) Close() error {
	s.fail(ErrSessionClosed)
	return s.conn.Close()
}

// Done is closed when the session ends (peer disconnect or Close).
func (s *Session) Done() <-chan struct{} { return s.closed }

func (s *Session) err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closeErr != nil {
		return s.closeErr
	}
	return ErrSessionClosed
}

func (s *Session) writeFrame(id uint32, typ byte, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(buf, id)
	buf[4] = typ
	copy(buf[5:], payload)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.closed:
		return ErrSessionClosed
	default:
	}
	return s.conn.WriteMessage(binaryMessage, buf)
}

func (s *Session) getStream(id uint32) *Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[id]
}

func (s *Session) removeStream(id uint32) {
	s.mu.Lock()
	delete(s.streams, id)
	s.mu.Unlock()
}

// fail closes the session with err and wakes every waiting stream. Idempotent.
func (s *Session) fail(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.closeErr = err
		streams := make([]*Stream, 0, len(s.streams))
		for _, st := range s.streams {
			streams = append(streams, st)
		}
		s.streams = map[uint32]*Stream{}
		close(s.closed)
		s.mu.Unlock()
		for _, st := range streams {
			st.setErr(err)
		}
	})
}

func (s *Session) readLoop() {
	for {
		mt, data, err := s.conn.ReadMessage()
		if err != nil {
			s.fail(err)
			return
		}
		if mt != binaryMessage || len(data) < 5 {
			continue
		}
		id := binary.BigEndian.Uint32(data[:4])
		typ := data[4]
		payload := data[5:]

		switch typ {
		case frameOpen:
			meta := append([]byte(nil), payload...)
			st := newStream(s, id, meta)
			s.mu.Lock()
			s.streams[id] = st
			s.mu.Unlock()
			select {
			case s.acceptCh <- st:
			case <-s.closed:
			}
		case frameData:
			if st := s.getStream(id); st != nil {
				st.recv(payload)
			}
		case frameWindow:
			if st := s.getStream(id); st != nil && len(payload) >= 4 {
				st.grantSend(int(binary.BigEndian.Uint32(payload)))
			}
		case frameEOF:
			if st := s.getStream(id); st != nil {
				st.recvEOF()
			}
		case frameReset:
			if st := s.getStream(id); st != nil {
				st.setErr(errors.New("runnertunnel: stream reset: " + string(payload)))
				s.removeStream(id)
			}
		}
	}
}

var _ io.ReadWriteCloser = (*Stream)(nil)
