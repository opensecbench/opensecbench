package analyst

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// A routed run must attribute usage to the provider that actually ran, not the active provider — and
// fall back to the routed provider's configured model when the routing entry names no model.
func TestTargetForTagAttribution(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	active := &llm.MockProvider{}
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", active)

	routed := &llm.MockProvider{}
	reg, err := db.CreateProvider(ctx, model.Provider{Name: "Local Qwen", Type: "ollama", Model: "qwen2.5:14b"})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetProviderResolver(func(_ context.Context, id string) (llm.Provider, error) {
		if id == reg.ID {
			return routed, nil
		}
		return nil, nil
	})

	// No tag → active provider, no attribution stamped (caller fills it from the active info).
	if tgt := svc.targetForTag(ctx, ""); tgt.Provider != active || tgt.ProviderName != "" || tgt.AttrModel != "" {
		t.Fatalf("no tag should use active with blank attribution, got %+v", tgt)
	}

	// Routing with an explicit model: attribution carries the provider type + that model.
	if err := db.SetSetting(ctx, ModelRoutingSetting, `{"tags":{"cheap":{"provider_id":"`+reg.ID+`","model":"qwen-custom"}}}`); err != nil {
		t.Fatal(err)
	}
	tgt := svc.targetForTag(ctx, "cheap")
	if tgt.Provider != routed {
		t.Fatal("cheap should route to the registered provider")
	}
	if tgt.ProviderName != "ollama" || tgt.AttrModel != "qwen-custom" || tgt.SessionModel != "qwen-custom" {
		t.Fatalf("attribution = %+v, want ollama/qwen-custom", tgt)
	}

	// Routing without a model: the session model stays blank (provider default), but attribution falls
	// back to the provider's configured model so the usage record still names a concrete model.
	if err := db.SetSetting(ctx, ModelRoutingSetting, `{"tags":{"cheap":{"provider_id":"`+reg.ID+`"}}}`); err != nil {
		t.Fatal(err)
	}
	tgt = svc.targetForTag(ctx, "cheap")
	if tgt.SessionModel != "" {
		t.Fatalf("session model should stay blank (provider default), got %q", tgt.SessionModel)
	}
	if tgt.ProviderName != "ollama" || tgt.AttrModel != "qwen2.5:14b" {
		t.Fatalf("attribution fallback = %+v, want ollama/qwen2.5:14b (configured default)", tgt)
	}
}
