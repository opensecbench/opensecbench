package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/events"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/proxy"
)

// arm compiles rules and installs them on the manager (panics on a bad rule — tests use valid rules).
func arm(t *testing.T, m *interceptManager, rules ...model.TrafficRule) {
	t.Helper()
	compiled, err := compileTrafficRules(rules)
	if err != nil {
		t.Fatalf("compile rules: %v", err)
	}
	m.setRules(compiled)
}

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

func TestEnabledReflectsRules(t *testing.T) {
	m := newInterceptManager("p", events.NewHub())
	if r, s := m.Enabled(); r || s {
		t.Fatal("no rules → nothing armed")
	}
	arm(t, m, model.TrafficRule{Enabled: true, Phase: model.RulePhaseRequest, Action: model.ActionHold})
	if r, s := m.Enabled(); !r || s {
		t.Fatalf("request rule → requests armed only, got req=%v resp=%v", r, s)
	}
	arm(t, m, model.TrafficRule{Enabled: false, Phase: model.RulePhaseBoth, Action: model.ActionHold})
	if r, s := m.Enabled(); r || s {
		t.Fatal("disabled rule should not arm")
	}
}

func TestHoldRuleResolveForward(t *testing.T) {
	m := newInterceptManager("p", events.NewHub())
	arm(t, m, model.TrafficRule{Enabled: true, Phase: model.RulePhaseRequest, Action: model.ActionHold})

	got := make(chan proxy.Decision, 1)
	go func() {
		got <- m.Hold(context.Background(), proxy.Held{Phase: proxy.PhaseRequest, URL: "http://x"})
	}()

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
		t.Fatal("hold not removed after resolve")
	}
	if m.resolve("nope", proxy.Decision{}) {
		t.Fatal("resolving an unknown id should return false")
	}
}

func TestNoMatchingRuleForwardsUnchanged(t *testing.T) {
	m := newInterceptManager("p", events.NewHub())
	arm(t, m, model.TrafficRule{Enabled: true, Phase: model.RulePhaseRequest, Match: `host == "other.example"`, Action: model.ActionHold})
	// URL host doesn't match the rule → Hold returns immediately, unchanged, no queue entry.
	d := m.Hold(context.Background(), proxy.Held{Phase: proxy.PhaseRequest, URL: "http://acme.example/x", Method: "GET"})
	if d.Drop || d.Method != "GET" || d.URL != "http://acme.example/x" {
		t.Fatalf("expected forward-unchanged, got %+v", d)
	}
	if len(m.stateView().Held) != 0 {
		t.Fatal("nothing should be held")
	}
}

func TestDropAction(t *testing.T) {
	m := newInterceptManager("p", events.NewHub())
	arm(t, m, model.TrafficRule{Enabled: true, Phase: model.RulePhaseRequest, Match: `path.matches("\\.png$")`, Action: model.ActionDrop})
	d := m.Hold(context.Background(), proxy.Held{Phase: proxy.PhaseRequest, URL: "http://x/logo.png"})
	if !d.Drop {
		t.Fatal("matching drop rule should drop")
	}
}

func TestModifyActionsFallThrough(t *testing.T) {
	m := newInterceptManager("p", events.NewHub())
	arm(t, m,
		model.TrafficRule{Enabled: true, Phase: model.RulePhaseRequest, Action: model.ActionSetHeader,
			Params: model.TrafficRuleParams{HeaderName: "X-Test", HeaderValue: "1"}},
		model.TrafficRule{Enabled: true, Phase: model.RulePhaseRequest, Match: `content_type.contains("json")`, Action: model.ActionReplaceBody,
			Params: model.TrafficRuleParams{Pattern: `"role":"user"`, Replacement: `"role":"admin"`}},
	)
	d := m.Hold(context.Background(), proxy.Held{
		Phase: proxy.PhaseRequest, Method: "POST", URL: "http://x/api",
		RequestHeaders: "Content-Type: application/json\n", RequestBody: `{"role":"user"}`,
	})
	if d.Drop {
		t.Fatal("modify rules should not drop")
	}
	if !strings.Contains(d.RequestHeaders, "X-Test: 1") {
		t.Fatalf("set_header not applied: %q", d.RequestHeaders)
	}
	if !strings.Contains(d.RequestBody, `"role":"admin"`) {
		t.Fatalf("replace_body not applied: %q", d.RequestBody)
	}
}

func TestDrainDrops(t *testing.T) {
	m := newInterceptManager("p", events.NewHub())
	arm(t, m, model.TrafficRule{Enabled: true, Phase: model.RulePhaseBoth, Action: model.ActionHold})
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

func TestCtxCancelDrops(t *testing.T) {
	m := newInterceptManager("p", events.NewHub())
	arm(t, m, model.TrafficRule{Enabled: true, Phase: model.RulePhaseBoth, Action: model.ActionHold})
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
