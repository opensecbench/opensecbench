package analyst

import (
	"context"
	"errors"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/llm"
)

func TestProviderModelForTag(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	active := &llm.MockProvider{}
	svc := NewService(db, nil, nil, "", active)

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
