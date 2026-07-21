package llm

import (
	"context"
	"errors"
	"strings"
)

// FallbackEntry is one (provider, model) in a fallback chain (ADR-0052).
type FallbackEntry struct {
	Provider Provider
	Model    string
}

// FallbackProvider tries an ordered list of (provider, model) entries, falling through to the next only
// when a call fails with a transient/availability error — e.g. a subscription is rate-limited or a
// backend is down (ADR-0052). A terminal error (bad request, content refusal) is returned as-is, since
// the next provider would reject it identically. It reports the first entry's tool mode and skips
// entries with a different mode, so the conversation rendering stays consistent across a fall-through.
type FallbackProvider struct {
	Entries []FallbackEntry
}

// Name identifies the provider.
func (f *FallbackProvider) Name() string { return "fallback" }

// NativeTools reports the first entry's tool mode (the mode the whole chain renders in).
func (f *FallbackProvider) NativeTools() bool {
	if len(f.Entries) == 0 {
		return false
	}
	return nativeToolsOf(f.Entries[0].Provider)
}

// Complete tries each entry in priority order, using that entry's model, and falls through on a
// transient error.
func (f *FallbackProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if len(f.Entries) == 0 {
		return CompletionResponse{}, errors.New("llm fallback: no entries")
	}
	mode := nativeToolsOf(f.Entries[0].Provider)
	var lastErr error
	for _, e := range f.Entries {
		if e.Provider == nil || nativeToolsOf(e.Provider) != mode {
			continue // keep the conversation rendered in one tool mode
		}
		r := req
		r.Model = e.Model
		resp, err := e.Provider.Complete(ctx, r)
		if err == nil {
			if resp.Model == "" {
				resp.Model = e.Model
			}
			return resp, nil
		}
		lastErr = err
		if !isFallthroughError(err) {
			return resp, err // terminal — the next provider would reject it the same way
		}
	}
	return CompletionResponse{}, lastErr
}

// nativeToolsOf reports a provider's tool mode (false when it doesn't advertise one).
func nativeToolsOf(p Provider) bool {
	if n, ok := p.(interface{ NativeTools() bool }); ok {
		return n.NativeTools()
	}
	return false
}

// fallthroughSignals are substrings marking a transient/availability failure worth retrying elsewhere,
// as opposed to a request the next provider would also reject.
var fallthroughSignals = []string{
	"429", "rate", "quota", "overload", "unavailable", "timeout", "deadline",
	"connection", "no such host", "dial ", "eof", "reset by peer",
	" 500", " 502", " 503", " 504", "401", "403", "unauthorized", "forbidden",
}

// isFallthroughError reports whether an error is transient/availability (fall through to the next entry)
// rather than terminal (fail the call). Heuristic string match — our adapters surface HTTP status and
// network errors in the message.
func isFallthroughError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, sig := range fallthroughSignals {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
}
