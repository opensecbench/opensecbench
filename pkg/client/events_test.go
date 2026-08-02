package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestBearerTokenOnEveryRequest confirms the transport wrapper attaches the ADR-0061 token to requests
// and that a token-less client sends no Authorization header (so an unauthenticated daemon still works).
func TestBearerTokenOnEveryRequest(t *testing.T) {
	var mu sync.Mutex
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer srv.Close()
	ctx := context.Background()

	if _, err := New(srv.URL, WithToken("s3cret")).Health(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := gotAuth
	mu.Unlock()
	if got != "Bearer s3cret" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer s3cret")
	}

	if _, err := New(srv.URL).Health(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got = gotAuth
	mu.Unlock()
	if got != "" {
		t.Fatalf("token-less client sent Authorization = %q, want empty", got)
	}
}

// TestAttachStreamsEvents drives the real SSE wire: the client connects (with its bearer token),
// ignores the connect comment, and yields typed events with a raw, decodable payload.
func TestAttachStreamsEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("stream missing bearer token: %q", r.Header.Get("Authorization"))
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": connected\n\n")
		fl.Flush()
		_, _ = io.WriteString(w, "event: exchange\ndata: {\"type\":\"exchange\",\"project_id\":\"p1\",\"payload\":{\"id\":\"e1\"}}\n\n")
		fl.Flush()
		_, _ = io.WriteString(w, "event: finding\ndata: {\"type\":\"finding\",\"payload\":{\"id\":\"f1\"}}\n\n")
		fl.Flush()
		<-r.Context().Done() // hold the stream open until the client detaches
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := New(srv.URL, WithToken("tok")).Attach(ctx, "p1")

	if e := recv(t, ch); e.Type != "exchange" || e.ProjectID != "p1" {
		t.Fatalf("event 1 = %+v, want exchange/p1", e)
	}
	e := recv(t, ch)
	if e.Type != "finding" {
		t.Fatalf("event 2 type = %q, want finding", e.Type)
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil || p.ID != "f1" {
		t.Fatalf("payload decode = %+v, err = %v", p, err)
	}

	cancel()
	if _, open := <-ch; open {
		// Drain until closed; a lingering open channel after cancel is the failure.
		for range ch {
		}
	}
}

// TestAttachReconnects confirms the stream self-heals: when the server ends a connection, the client
// dials again (after backoff) and keeps delivering — the property SSH resilience depends on.
func TestAttachReconnects(t *testing.T) {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		k := n
		mu.Unlock()
		fl := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: tick\ndata: {\"type\":\"tick\",\"payload\":%d}\n\n", k)
		fl.Flush()
		// Return immediately: the connection closes and the client must reconnect for the next tick.
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := New(srv.URL).Attach(ctx, "p1")

	if got := tickPayload(t, recv(t, ch)); got != 1 {
		t.Fatalf("first tick = %d, want 1", got)
	}
	if got := tickPayload(t, recv(t, ch)); got != 2 {
		t.Fatalf("second tick (after reconnect) = %d, want 2", got)
	}
}

func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("event channel closed early")
		}
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func tickPayload(t *testing.T, e Event) int {
	t.Helper()
	if e.Type != "tick" {
		t.Fatalf("type = %q, want tick", e.Type)
	}
	var v int
	if err := json.Unmarshal(e.Payload, &v); err != nil {
		t.Fatalf("decode tick payload: %v", err)
	}
	return v
}
