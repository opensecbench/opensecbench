package api

import (
	"context"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/events"
	"github.com/opensecbench/opensecbench/pkg/proxy"
)

// waitHeld waits until the manager reports exactly one held item and returns its id.
func waitHeld(t *testing.T, m *interceptManager) string {
	t.Helper()
	for i := 0; i < 200; i++ {
		if st := m.stateView(); len(st.Held) == 1 {
			return st.Held[0].ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("hold never registered")
	return ""
}

func TestInterceptManagerResolveForward(t *testing.T) {
	m := newInterceptManager("p", events.NewHub())
	m.setEnabled(true, false)
	if r, _ := m.Enabled(); !r {
		t.Fatal("requests should be armed")
	}

	got := make(chan proxy.Decision, 1)
	go func() { got <- m.Hold(context.Background(), proxy.Held{Phase: proxy.PhaseRequest, URL: "http://x"}) }()

	id := waitHeld(t, m)
	if !m.resolve(id, proxy.Decision{Method: "PATCHED"}) {
		t.Fatal("resolve returned false")
	}
	select {
	case d := <-got:
		if d.Method != "PATCHED" {
			t.Fatalf("decision = %+v, want the forwarded edit", d)
		}
	case <-time.After(time.Second):
		t.Fatal("Hold did not return after resolve")
	}
	if len(m.stateView().Held) != 0 {
		t.Fatal("hold not removed from the queue after resolve")
	}
	if m.resolve("nope", proxy.Decision{}) {
		t.Fatal("resolving an unknown id should return false")
	}
}

func TestInterceptManagerDrainDrops(t *testing.T) {
	m := newInterceptManager("p", events.NewHub())
	got := make(chan proxy.Decision, 1)
	go func() { got <- m.Hold(context.Background(), proxy.Held{Phase: proxy.PhaseRequest}) }()
	waitHeld(t, m)
	m.drain()
	select {
	case d := <-got:
		if !d.Drop {
			t.Fatal("drain should release the hold as a drop")
		}
	case <-time.After(time.Second):
		t.Fatal("drain did not release the hold")
	}
}

func TestInterceptManagerCtxCancelDrops(t *testing.T) {
	m := newInterceptManager("p", events.NewHub())
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan proxy.Decision, 1)
	go func() { got <- m.Hold(ctx, proxy.Held{Phase: proxy.PhaseRequest}) }()
	waitHeld(t, m)
	cancel()
	select {
	case d := <-got:
		if !d.Drop {
			t.Fatal("client disconnect (ctx cancel) should drop the hold")
		}
	case <-time.After(time.Second):
		t.Fatal("ctx cancel did not release the hold")
	}
}
