package llm

import (
	"bufio"
	"context"
	"io"
	"strings"
)

// StreamHandler receives text as a completion is generated, one delta at a time (token streaming).
type StreamHandler func(delta string)

// StreamingProvider is an optional capability: a provider that can emit text deltas as it generates a
// completion, in addition to returning the full result. Providers that don't implement it still work via
// Stream (which falls back to a single whole-text delta), so callers stream generically across all backends.
type StreamingProvider interface {
	Provider
	CompleteStream(ctx context.Context, req CompletionRequest, onDelta StreamHandler) (CompletionResponse, error)
}

// Stream generates a completion, delivering its text incrementally to onDelta. If the provider implements
// StreamingProvider it streams real token deltas; otherwise it falls back to Complete and delivers the whole
// text as one delta — so this works for ANY provider (a non-streaming or prompted backend just yields one
// chunk). onDelta nil ⇒ a plain Complete. The returned CompletionResponse is always the full result.
func Stream(ctx context.Context, p Provider, req CompletionRequest, onDelta StreamHandler) (CompletionResponse, error) {
	if onDelta == nil {
		return p.Complete(ctx, req)
	}
	if sp, ok := p.(StreamingProvider); ok {
		return sp.CompleteStream(ctx, req, onDelta)
	}
	resp, err := p.Complete(ctx, req)
	if err == nil && resp.Text != "" {
		onDelta(resp.Text)
	}
	return resp, err
}

// sseData invokes fn for each `data:` payload in a Server-Sent Events stream until EOF or fn returns false.
// Blank lines and non-data lines (event:, id:, `:` comments) are skipped. The streaming providers share this
// and parse the JSON payloads in their own wire format (Anthropic message_* events, OpenAI chat chunks).
func sseData(r io.Reader, fn func(data string) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // a single delta line can be large; allow up to 4MiB
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "" {
			continue
		}
		if !fn(data) {
			return nil
		}
	}
	return sc.Err()
}
