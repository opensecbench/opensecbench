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

	"github.com/opensecbench/opensecbench/pkg/api"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
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

// TestProjectThreadsSendProjectHeader confirms the project-scoped thread calls carry X-Project-Id (the
// routing key that homes a thread in its project's database, ADR-0049) — verified at the wire so it
// doesn't depend on store isolation the test harness collapses.
func TestProjectThreadsSendProjectHeader(t *testing.T) {
	type seen struct {
		method, path, project string
	}
	var mu sync.Mutex
	got := map[string]seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.Method+" "+trimID(r.URL.Path)] = seen{r.Method, r.URL.Path, r.Header.Get("X-Project-Id")}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	c := New(srv.URL)
	ctx := context.Background()

	_, _ = c.ProjectThreads(ctx, "proj-9")
	_, _ = c.CreateThread(ctx, "proj-9", "chat")
	_, _ = c.ProjectThread(ctx, "proj-9", "th-1")
	_, _ = c.SendToThread(ctx, "proj-9", "th-1", "hi")

	mu.Lock()
	defer mu.Unlock()
	for _, key := range []string{"GET /v1/threads", "POST /v1/threads", "GET /v1/threads/th-1", "POST /v1/threads/th-1/messages"} {
		if s, ok := got[key]; !ok {
			t.Errorf("%s was not called", key)
		} else if s.project != "proj-9" {
			t.Errorf("%s sent X-Project-Id=%q, want proj-9", key, s.project)
		}
	}
}

// trimID collapses the trailing id segment so the four thread routes map to stable keys.
func trimID(path string) string {
	switch {
	case path == "/v1/threads/th-1/messages":
		return "/v1/threads/th-1/messages"
	case path == "/v1/threads/th-1":
		return "/v1/threads/th-1"
	default:
		return path
	}
}

// TestProjectThreadRoundTrip drives the real API handler: create a thread in a project, list it, fetch
// it back with history — proving the requests are well-formed and the responses decode.
func TestProjectThreadRoundTrip(t *testing.T) {
	c := newServer(t)
	ctx := context.Background()

	p, err := c.CreateProject(ctx, CreateProjectRequest{Name: "TUI threads"})
	if err != nil {
		t.Fatal(err)
	}
	th, err := c.CreateThread(ctx, p.ID, "chat")
	if err != nil || th.ID == "" {
		t.Fatalf("CreateThread = %+v, err = %v", th, err)
	}
	list, err := c.ProjectThreads(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != th.ID {
		t.Fatalf("ProjectThreads = %+v, want [%s]", list, th.ID)
	}
	detail, err := c.ProjectThread(ctx, p.ID, th.ID)
	if err != nil || detail.Thread.ID != th.ID {
		t.Fatalf("ProjectThread = %+v, err = %v", detail, err)
	}
}

// TestAttachReceivesPublishedEvent closes the full loop over the real wire: a domain event published
// through the API server's Publish seam (the one the engine uses) reaches an Attach subscriber and
// decodes. Publishing in a loop covers the subscribe/connect race — the hub drops events with no
// subscriber, so we retry until the stream is live.
func TestAttachReceivesPublishedEvent(t *testing.T) {
	db := storetest.New(t)
	srv := api.New(api.Deps{Store: store.NewCombinedManager(db)})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := New(ts.URL).Attach(ctx, "p1")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		srv.Publish("p1", "finding.created", model.Finding{Title: "SQLi", Severity: "high"})
		select {
		case e := <-ch:
			if e.Type != "finding.created" {
				t.Fatalf("event type = %q, want finding.created", e.Type)
			}
			var f model.Finding
			if err := json.Unmarshal(e.Payload, &f); err != nil || f.Title != "SQLi" {
				t.Fatalf("payload decode = %+v, err = %v", f, err)
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("published event never arrived over the stream")
}

// TestFindingsObservationsEndpoints confirms the /findings and project /observations reads the TUI's
// /findings and /observations commands depend on are wired and decode against the real handler.
func TestFindingsObservationsEndpoints(t *testing.T) {
	c := newServer(t)
	ctx := context.Background()
	p, err := c.CreateProject(ctx, CreateProjectRequest{Name: "obs"})
	if err != nil {
		t.Fatal(err)
	}
	if obs, err := c.ProjectObservations(ctx, p.ID); err != nil {
		t.Fatalf("ProjectObservations: %v", err)
	} else if len(obs) != 0 {
		t.Fatalf("want 0 observations for a fresh project, got %d", len(obs))
	}
	if _, err := c.ListFindings(ctx); err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if _, err := c.ProjectSearch(ctx, p.ID, "anything"); err != nil {
		t.Fatalf("ProjectSearch: %v", err)
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
