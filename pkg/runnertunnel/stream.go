package runnertunnel

import (
	"io"
	"sync"
)

// Stream is one multiplexed logical connection. It is an io.ReadWriteCloser: Write sends DATA (respecting
// the peer's credit window), Read yields received DATA and grants the peer more credit as bytes are
// consumed. CloseWrite half-closes the write direction (EOF); Close tears the stream down.
type Stream struct {
	id   uint32
	meta []byte
	sess *Session

	mu   sync.Mutex
	cond *sync.Cond

	inbuf   []byte // received, not yet read
	readEOF bool   // peer sent EOF
	err     error  // reset / session failure

	sendWindow  int // credit to send DATA to the peer
	writeClosed bool
	closed      bool
}

func newStream(sess *Session, id uint32, meta []byte) *Stream {
	st := &Stream{id: id, meta: meta, sess: sess, sendWindow: initialWindow}
	st.cond = sync.NewCond(&st.mu)
	return st
}

// Meta is the metadata the opener attached (OPEN payload).
func (st *Stream) Meta() []byte { return st.meta }

// ID is the stream's identifier within its session.
func (st *Stream) ID() uint32 { return st.id }

func (st *Stream) recv(p []byte) {
	st.mu.Lock()
	if st.err == nil && !st.closed {
		st.inbuf = append(st.inbuf, p...)
	}
	st.cond.Broadcast()
	st.mu.Unlock()
}

func (st *Stream) recvEOF() {
	st.mu.Lock()
	st.readEOF = true
	st.cond.Broadcast()
	st.mu.Unlock()
}

func (st *Stream) grantSend(n int) {
	st.mu.Lock()
	st.sendWindow += n
	st.cond.Broadcast()
	st.mu.Unlock()
}

func (st *Stream) setErr(err error) {
	st.mu.Lock()
	if st.err == nil {
		st.err = err
	}
	st.cond.Broadcast()
	st.mu.Unlock()
}

// Read yields received bytes, blocking until some arrive, the peer half-closes (io.EOF), or the stream
// errors. Each consumed chunk grants the peer that many bytes of fresh credit.
func (st *Stream) Read(p []byte) (int, error) {
	st.mu.Lock()
	for len(st.inbuf) == 0 && !st.readEOF && st.err == nil {
		st.cond.Wait()
	}
	if len(st.inbuf) > 0 {
		n := copy(p, st.inbuf)
		st.inbuf = st.inbuf[n:]
		st.mu.Unlock()
		_ = st.sess.writeFrame(st.id, frameWindow, uint32Bytes(n)) // return credit
		return n, nil
	}
	err := st.err
	st.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return 0, io.EOF
}

// Write sends p as DATA frames, blocking while the peer's credit window is exhausted.
func (st *Stream) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		st.mu.Lock()
		for st.sendWindow == 0 && st.err == nil && !st.writeClosed && !st.closed {
			st.cond.Wait()
		}
		if st.err != nil {
			st.mu.Unlock()
			return total, st.err
		}
		if st.writeClosed || st.closed {
			st.mu.Unlock()
			return total, io.ErrClosedPipe
		}
		n := len(p)
		if n > st.sendWindow {
			n = st.sendWindow
		}
		if n > maxFrame {
			n = maxFrame
		}
		st.sendWindow -= n
		st.mu.Unlock()

		if err := st.sess.writeFrame(st.id, frameData, p[:n]); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

// CloseWrite half-closes the write direction (sends EOF); reads continue.
func (st *Stream) CloseWrite() error {
	st.mu.Lock()
	if st.writeClosed {
		st.mu.Unlock()
		return nil
	}
	st.writeClosed = true
	st.mu.Unlock()
	return st.sess.writeFrame(st.id, frameEOF, nil)
}

// Close tears the stream down. If the read side ended normally it just half-closes writes; otherwise it
// resets the stream so the peer stops.
func (st *Stream) Close() error {
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return nil
	}
	st.closed = true
	needReset := st.err == nil && !st.readEOF
	st.mu.Unlock()

	if needReset {
		_ = st.sess.writeFrame(st.id, frameReset, nil)
	} else {
		_ = st.CloseWrite()
	}
	st.sess.removeStream(st.id)
	st.setErr(io.ErrClosedPipe)
	return nil
}

func uint32Bytes(n int) []byte {
	return []byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
}
