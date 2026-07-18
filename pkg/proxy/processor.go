package proxy

import "net/http"

// Processor transforms traffic in flight — match/replace, redaction, tagging. It is the toolset's
// second extension seam (ADR-0016). Unlike Interceptor it never blocks: it runs on the hot path and
// must be fast. Headers are the same "Key: value\n" text used elsewhere so a processor edits them as
// plain text. A nil processor is a no-op.
type Processor interface {
	// NeedsResponseBody reports whether any active rule targets the response, so the proxy buffers the
	// upstream body (streaming stays the default otherwise).
	NeedsResponseBody() bool
	ProcessRequest(method, url, headers, body string) (m, u, h, b string)
	ProcessResponse(status int, headers, body string) (s int, h, b string)
}

type noopProcessor struct{}

func (noopProcessor) NeedsResponseBody() bool { return false }
func (noopProcessor) ProcessRequest(m, u, h, b string) (string, string, string, string) {
	return m, u, h, b
}
func (noopProcessor) ProcessResponse(s int, h, b string) (int, string, string) { return s, h, b }

// applyRequestRules runs the traffic-processor over the outgoing request (auto match/replace),
// bridging the proxy's http.Header to the string-header form processors use.
func (p *Proxy) applyRequestRules(method, url string, header http.Header, body []byte) (string, string, http.Header, []byte) {
	m, u, h, b := p.process.ProcessRequest(method, url, formatHeaders(header), string(body))
	return m, u, parseHeaders(h), []byte(b)
}

func (p *Proxy) applyResponseRules(status int, header http.Header, body []byte) (int, http.Header, []byte) {
	s, h, b := p.process.ProcessResponse(status, formatHeaders(header), string(body))
	return s, parseHeaders(h), []byte(b)
}
