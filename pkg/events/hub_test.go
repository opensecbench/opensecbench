package events

import (
	"testing"
	"time"
)

func recv(t *testing.T, ch <-chan Event) (Event, bool) {
	t.Helper()
	select {
	case e := <-ch:
		return e, true
	case <-time.After(200 * time.Millisecond):
		return Event{}, false
	}
}

func TestHubRoutingAndFilter(t *testing.T) {
	h := NewHub()
	a, unsubA := h.Subscribe("proj-a")
	defer unsubA()
	all, unsubAll := h.Subscribe("")
	defer unsubAll()

	// An event for proj-a reaches the proj-a subscriber and the global subscriber.
	h.Publish(Event{Type: "exchange", ProjectID: "proj-a", Payload: 1})
	if e, ok := recv(t, a); !ok || e.Payload != 1 {
		t.Fatalf("proj-a subscriber missed its event: %v %v", e, ok)
	}
	if _, ok := recv(t, all); !ok {
		t.Fatal("global subscriber missed the event")
	}

	// An event for a different project does not reach the proj-a subscriber.
	h.Publish(Event{Type: "exchange", ProjectID: "proj-b"})
	if e, ok := recv(t, a); ok {
		t.Fatalf("proj-a subscriber got a foreign event: %v", e)
	}
	if _, ok := recv(t, all); !ok {
		t.Fatal("global subscriber missed the proj-b event")
	}

	// A global event ("") reaches project subscribers.
	h.Publish(Event{Type: "proxy", ProjectID: ""})
	if _, ok := recv(t, a); !ok {
		t.Fatal("proj-a subscriber missed the global event")
	}
}

func TestHubUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe("p")
	unsub()
	unsub() // safe to call twice
	h.Publish(Event{Type: "x", ProjectID: "p"})
	if _, ok := <-ch; ok {
		t.Fatal("expected closed channel with no delivery after unsubscribe")
	}
}

func TestHubPublishNeverBlocks(t *testing.T) {
	h := NewHub()
	_, unsub := h.Subscribe("p") // never drained
	defer unsub()
	done := make(chan struct{})
	go func() {
		for i := 0; i < buffer*4; i++ {
			h.Publish(Event{Type: "x", ProjectID: "p"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}
