package dlp

import (
	"context"
	"fmt"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/llm"
)

// Loader returns the current secret and canary sets to scan against (fetched per call so newly
// added secrets/canaries take effect immediately).
type Loader func(ctx context.Context) (secrets, canaries map[string]string)

// GuardedProvider wraps an llm.Provider, scanning outbound message content before it is sent. On an
// external provider a block-worthy hit (vault secret or canary) aborts the request; pattern hits are
// recorded but allowed. Every hit is reported via onHit for the dlp_event trail.
type GuardedProvider struct {
	inner    llm.Provider
	external bool
	load     Loader
	onHit    func(ctx context.Context, h Hit, blocked bool)
}

// Guard wraps inner with DLP inspection. external marks whether the backend leaves the machine.
func Guard(inner llm.Provider, external bool, load Loader, onHit func(context.Context, Hit, bool)) *GuardedProvider {
	if load == nil {
		load = func(context.Context) (map[string]string, map[string]string) { return nil, nil }
	}
	if onHit == nil {
		onHit = func(context.Context, Hit, bool) {}
	}
	return &GuardedProvider{inner: inner, external: external, load: load, onHit: onHit}
}

// Unwrap exposes the wrapped provider so classifiers (e.g. llm.IsLocal) see the real backend.
func (g *GuardedProvider) Unwrap() llm.Provider { return g.inner }

// Name reports the underlying provider's name.
func (g *GuardedProvider) Name() string { return g.inner.Name() }

// Complete scans the request's message content, then delegates (unless blocked).
func (g *GuardedProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	var sb strings.Builder
	for _, m := range req.Messages {
		sb.WriteString(m.Content)
		sb.WriteByte('\n')
	}
	secrets, canaries := g.load(ctx)
	hits := New(secrets, canaries).Inspect(sb.String())

	var blockedLabels []string
	for _, h := range hits {
		blocked := g.external && h.Action == ActionBlock
		g.onHit(ctx, h, blocked)
		if blocked {
			blockedLabels = append(blockedLabels, h.Kind+":"+h.Label)
		}
	}
	if len(blockedLabels) > 0 {
		return llm.CompletionResponse{}, fmt.Errorf("blocked by DLP: %s must not be sent to external provider %q",
			strings.Join(blockedLabels, ", "), g.inner.Name())
	}
	return g.inner.Complete(ctx, req)
}
