package analyst

import (
	"context"
	"errors"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestProviderModelForTag(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	active := &llm.MockProvider{}
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", active)

	// No resolver / no routing → the active provider, no model override.
	if p, m := svc.providerModelForTag(ctx, "cheap"); p != active || m != "" {
		t.Fatalf("no routing should use the active provider, got %v/%q", p, m)
	}

	routed := &llm.MockProvider{} // a distinct instance to prove routing switched providers
	svc.SetProviderResolver(func(_ context.Context, id string) (llm.Provider, error) {
		if id == "prov-x" {
			return routed, nil
		}
		return nil, errors.New("unknown provider")
	})

	// A mapped tag routes to its (provider, model).
	if err := db.SetSetting(ctx, ModelRoutingSetting, `{"tags":{"cheap":{"provider_id":"prov-x","model":"qwen2.5:14b"}}}`); err != nil {
		t.Fatal(err)
	}
	if p, m := svc.providerModelForTag(ctx, "cheap"); p != routed || m != "qwen2.5:14b" {
		t.Fatalf("cheap should route to prov-x/qwen, got %v/%q", p, m)
	}
	// An unmapped tag with no default falls back to the active provider.
	if p, _ := svc.providerModelForTag(ctx, "reasoning"); p != active {
		t.Fatal("unmapped tag without a default should fall back to active")
	}

	// A default catches unmapped tags.
	if err := db.SetSetting(ctx, ModelRoutingSetting, `{"default":{"provider_id":"prov-x","model":"d"},"tags":{}}`); err != nil {
		t.Fatal(err)
	}
	if p, m := svc.providerModelForTag(ctx, "reasoning"); p != routed || m != "d" {
		t.Fatalf("default should apply, got %v/%q", p, m)
	}

	// A broken provider ref falls back to active (a run never fails to start on bad routing).
	if err := db.SetSetting(ctx, ModelRoutingSetting, `{"tags":{"cheap":{"provider_id":"nope","model":"z"}}}`); err != nil {
		t.Fatal(err)
	}
	if p, _ := svc.providerModelForTag(ctx, "cheap"); p != active {
		t.Fatal("an unresolvable provider ref should fall back to active")
	}
}

// The ordered-list shape (ADR-0052): a tag holds a priority list; the first resolvable entry wins, so an
// unresolvable top entry falls through to the next.
func TestRoutingOrderedListFallThrough(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	active := &llm.MockProvider{}
	sub := &llm.MockProvider{} // "subscription" (top choice)
	gw := &llm.MockProvider{}  // "gateway" fallback
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", active)
	svc.SetProviderResolver(func(_ context.Context, id string) (llm.Provider, error) {
		switch id {
		case "cli":
			return sub, nil
		case "bedrock":
			return gw, nil
		}
		return nil, errors.New("unknown provider")
	})

	// Priority: subscription first, gateway second. Two resolvable entries → a fallback chain that tries
	// the subscription first.
	if err := db.SetSetting(ctx, ModelRoutingSetting,
		`{"default":[{"provider_id":"cli","model":"claude-sonnet-5"},{"provider_id":"bedrock","model":"anthropic.claude-sonnet-4-5-v1:0"}]}`); err != nil {
		t.Fatal(err)
	}
	p, m := svc.providerModelForTag(ctx, "default")
	fb, ok := p.(*llm.FallbackProvider)
	if !ok || m != "claude-sonnet-5" {
		t.Fatalf("2-entry list should yield a fallback chain / top model, got %T / %q", p, m)
	}
	if len(fb.Entries) != 2 || fb.Entries[0].Provider != sub || fb.Entries[1].Provider != gw {
		t.Fatalf("chain order wrong: %+v", fb.Entries)
	}

	// Top entry unresolvable → falls through to the next in priority order.
	if err := db.SetSetting(ctx, ModelRoutingSetting,
		`{"default":[{"provider_id":"gone","model":"x"},{"provider_id":"bedrock","model":"anthropic.claude-sonnet-4-5-v1:0"}]}`); err != nil {
		t.Fatal(err)
	}
	if p, m := svc.providerModelForTag(ctx, "default"); p != gw || m != "anthropic.claude-sonnet-4-5-v1:0" {
		t.Fatalf("unresolvable top should fall through to gateway, got %v/%q", p, m)
	}

	// The Routing() view normalizes to ordered lists for the UI.
	view := svc.Routing(ctx)
	if len(view["default"]) != 2 || view["default"][1].ProviderID != "bedrock" {
		t.Fatalf("Routing() view wrong: %+v", view["default"])
	}
}

// The interactive chat runs on the generalist profile; it must carry a routing tag so the routing
// "default" row applies, instead of silently using the active provider's default model.
func TestGeneralistHonorsRoutingDefault(t *testing.T) {
	if tag := ProfileByID("generalist").ModelTag; tag == "" {
		t.Fatal("generalist has no ModelTag — the chat agent would bypass model routing")
	}

	ctx := context.Background()
	db := migratedStore(t)
	active := &llm.MockProvider{}
	routed := &llm.MockProvider{}
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", active)
	svc.SetProviderResolver(func(_ context.Context, id string) (llm.Provider, error) {
		if id == "cli-x" {
			return routed, nil
		}
		return nil, errors.New("unknown provider")
	})
	if err := db.SetSetting(ctx, ModelRoutingSetting, `{"default":{"provider_id":"cli-x","model":"claude-haiku-4-5"},"tags":{}}`); err != nil {
		t.Fatal(err)
	}
	// The generalist's tag must resolve to the routing default's (provider, model).
	if p, m := svc.providerModelForTag(ctx, ProfileByID("generalist").ModelTag); p != routed || m != "claude-haiku-4-5" {
		t.Fatalf("generalist should resolve the routing default, got %v/%q", p, m)
	}
}
