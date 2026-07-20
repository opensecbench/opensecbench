package proxy

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Phase identifies which side of an exchange is being held.
type Phase string

const (
	PhaseRequest  Phase = "request"
	PhaseResponse Phase = "response"
)

// Held is a request or response paused at the proxy awaiting an operator decision. Headers are the
// same "Key: value\n" text the rest of the toolset uses, so the operator edits them as plain text.
type Held struct {
	Phase           Phase
	Method          string
	URL             string
	RequestHeaders  string
	RequestBody     string
	Status          int
	ResponseHeaders string
	ResponseBody    string
}

// Decision resolves a Held: drop it, or forward with these (possibly edited) values. A request hold
// reads the method/url/request fields; a response hold reads the status/response fields.
type Decision struct {
	Drop            bool
	Method          string
	URL             string
	RequestHeaders  string
	RequestBody     string
	Status          int
	ResponseHeaders string
	ResponseBody    string
}

// Interceptor decides whether to hold traffic and blocks while the operator resolves a hold. The
// queue, control protocol, and events all live in the caller (pkg/api) — the proxy only respects the
// decision, keeping the concurrency out of the transport code.
type Interceptor interface {
	// Enabled reports whether the request and/or response phases should be held. It is called on the
	// hot path, so it must be cheap; false means "forward unchanged, don't buffer".
	Enabled() (requests, responses bool)
	// Hold blocks until the operator resolves h, or ctx is cancelled (which yields a drop).
	Hold(ctx context.Context, h Held) Decision
}

type noopInterceptor struct{}

func (noopInterceptor) Enabled() (bool, bool)               { return false, false }
func (noopInterceptor) Hold(context.Context, Held) Decision { return Decision{} }

// holdRequest blocks the request at the operator; returns the (possibly edited) values to send, or
// dropped. Only called when request interception is armed.
func (p *Proxy) holdRequest(ctx context.Context, method, url string, header http.Header, body []byte) (m, u string, h http.Header, b []byte, dropped bool) {
	d := p.intercept.Hold(ctx, Held{
		Phase: PhaseRequest, Method: method, URL: url,
		RequestHeaders: formatHeaders(header), RequestBody: string(body),
	})
	if d.Drop {
		return "", "", nil, nil, true
	}
	return d.Method, d.URL, parseHeaders(d.RequestHeaders), []byte(d.RequestBody), false
}

// holdResponse blocks a buffered response at the operator; returns the (possibly edited) values to
// deliver, or dropped. Only called when response interception is armed.
func (p *Proxy) holdResponse(ctx context.Context, method, url string, status int, header http.Header, body []byte) (st int, h http.Header, b []byte, dropped bool) {
	d := p.intercept.Hold(ctx, Held{
		Phase: PhaseResponse, Method: method, URL: url,
		Status: status, ResponseHeaders: formatHeaders(header), ResponseBody: string(body),
	})
	if d.Drop {
		return 0, nil, nil, true
	}
	return d.Status, parseHeaders(d.ResponseHeaders), []byte(d.ResponseBody), false
}

// capture records the exchange actually delivered (post-edit request + delivered response), so
// history and provenance reflect what really went over the wire.
func (p *Proxy) capture(method, url string, reqHeader http.Header, reqBody []byte, status int, respHeader http.Header, respBody []byte, start time.Time, tlsSummary string) {
	p.onExchange(Exchange{
		Method: method, URL: url,
		RequestHeaders: formatHeaders(reqHeader), RequestBody: capString(reqBody),
		Status: status, ResponseHeaders: formatHeaders(respHeader), ResponseBody: capString(respBody),
		DurationMS: int(time.Since(start).Milliseconds()),
		TLS:        tlsSummary,
	})
}

// fixRequestFraming drops copied length/encoding headers so net/http reframes from the actual body
// (which an operator edit may have resized). http.NewRequest already set outReq.ContentLength.
func fixRequestFraming(outReq *http.Request) {
	outReq.Header.Del("Content-Length")
	outReq.Header.Del("Transfer-Encoding")
}

// parseHeaders is the inverse of formatHeaders: "Key: value" lines back into an http.Header.
func parseHeaders(s string) http.Header {
	h := http.Header{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		h.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	return h
}
