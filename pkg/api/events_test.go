package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/events"
)

// TestProjectEventsSSE drives the real SSE wire: connect, confirm the stream opens, publish an event
// to the hub, and read it back off the stream framed as `event:`/`data:`.
func TestProjectEventsSSE(t *testing.T) {
	s := New(Deps{})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/projects/p1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	// Read lines off the stream in the background so reads can time out.
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	next := func() string {
		select {
		case l := <-lines:
			return l
		case <-time.After(2 * time.Second):
			t.Fatal("timed out reading SSE stream")
			return ""
		}
	}

	// The handler writes ": connected" only after it has subscribed, so this is a race-free signal
	// that publishing will now be delivered rather than dropped.
	for !strings.Contains(next(), "connected") {
	}

	s.events.Publish(events.Event{Type: "exchange", ProjectID: "p1", Payload: map[string]string{"id": "x1"}})

	var sawEvent, sawData bool
	deadline := time.After(2 * time.Second)
	for !(sawEvent && sawData) {
		select {
		case <-deadline:
			t.Fatalf("did not receive the published event (event=%v data=%v)", sawEvent, sawData)
		case l := <-lines:
			if l == "event: exchange" {
				sawEvent = true
			}
			if strings.HasPrefix(l, "data: ") && strings.Contains(l, `"id":"x1"`) {
				sawData = true
			}
		}
	}

	// An event for a different project must not reach this p1 subscriber.
	s.events.Publish(events.Event{Type: "exchange", ProjectID: "other", Payload: map[string]string{"id": "nope"}})
	select {
	case l := <-lines:
		if strings.Contains(l, "nope") {
			t.Fatal("received an event for a different project")
		}
	case <-time.After(300 * time.Millisecond):
		// expected: nothing for p1
	}
}
