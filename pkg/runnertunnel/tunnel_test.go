package runnertunnel

import (
	"bytes"
	"crypto/rand"
	"io"
	"sync"
	"testing"
	"time"
)

// pipeConn is an in-memory MessageConn pair for tests. A shared closed channel means closing either end
// surfaces EOF on both.
type pipeConn struct {
	in     chan []byte
	out    chan []byte
	closed chan struct{}
	once   *sync.Once
}

func newPipe() (*pipeConn, *pipeConn) {
	a2b := make(chan []byte, 256)
	b2a := make(chan []byte, 256)
	closed := make(chan struct{})
	once := &sync.Once{}
	a := &pipeConn{in: b2a, out: a2b, closed: closed, once: once}
	b := &pipeConn{in: a2b, out: b2a, closed: closed, once: once}
	return a, b
}

func (c *pipeConn) ReadMessage() (int, []byte, error) {
	select {
	case msg := <-c.in:
		return binaryMessage, msg, nil
	case <-c.closed:
		return 0, nil, io.EOF
	}
}

func (c *pipeConn) WriteMessage(_ int, data []byte) error {
	select {
	case c.out <- data:
		return nil
	case <-c.closed:
		return io.ErrClosedPipe
	}
}

func (c *pipeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

// echoServer accepts streams and echoes each request body back as the response.
func echoServer(t *testing.T, sess *Session) {
	for {
		st, err := sess.Accept()
		if err != nil {
			return
		}
		go func(st *Stream) {
			_, _ = io.Copy(st, st) // request bytes -> response bytes
			_ = st.CloseWrite()
		}(st)
	}
}

func TestTunnelEchoAndMeta(t *testing.T) {
	a, b := newPipe()
	client := New(a, true)
	server := New(b, false)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	go echoServer(t, server)

	st, err := client.Open([]byte("GET /x"))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = st.Write([]byte("hello tunnel"))
		_ = st.CloseWrite()
	}()
	got, err := io.ReadAll(st)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello tunnel" {
		t.Fatalf("echo = %q", got)
	}
}

// A transfer larger than the flow-control window must complete — proving credit windowing, not one big
// buffer, is what carries it.
func TestTunnelLargeTransferFlowControl(t *testing.T) {
	a, b := newPipe()
	client := New(a, true)
	server := New(b, false)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	go echoServer(t, server)

	payload := make([]byte, 4*initialWindow+123) // > window, forces WINDOW grants
	_, _ = rand.Read(payload)

	st, err := client.Open(nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = st.Write(payload)
		_ = st.CloseWrite()
	}()
	got, err := io.ReadAll(st)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("large transfer mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

// Concurrent streams must not interfere.
func TestTunnelConcurrentStreams(t *testing.T) {
	a, b := newPipe()
	client := New(a, true)
	server := New(b, false)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	go echoServer(t, server)

	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, err := client.Open(nil)
			if err != nil {
				errs <- err
				return
			}
			msg := bytes.Repeat([]byte{byte('A' + i)}, 5000)
			go func() { _, _ = st.Write(msg); _ = st.CloseWrite() }()
			got, err := io.ReadAll(st)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, msg) {
				t.Errorf("stream %d: echo mismatch", i)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestTunnelSessionCloseWakesWaiters(t *testing.T) {
	a, b := newPipe()
	client := New(a, true)
	server := New(b, false)
	defer func() { _ = server.Close() }()

	st, err := client.Open(nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := st.Read(make([]byte, 16))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	_ = client.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Read should error after the session closes")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not wake on session close")
	}
	if _, err := client.Open(nil); err == nil {
		t.Fatal("Open should fail on a closed session")
	}
}
