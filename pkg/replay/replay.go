// Package replay is the HTTP transport for the Replay (ADR-0007): it issues a single request
// and captures the response. It enforces no policy — the caller (the service layer) applies the
// scope guard and audit so every send is governed in one place.
package replay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// MaxBodyBytes caps a captured response body; larger bodies are truncated with a marker.
const MaxBodyBytes = 2 << 20 // 2 MiB

// Request is one HTTP request to send. Headers are "Key: value" lines, one per line.
type Request struct {
	Method  string
	URL     string
	Headers string
	Body    string
}

// Response is the captured result of a send.
type Response struct {
	Status     int
	Headers    string
	Body       string
	DurationMS int
}

// Client sends requests without following redirects (the operator sees each hop) under a bounded
// timeout.
type Client struct {
	http *http.Client
}

// New returns a Client with the given per-request timeout (0 uses a 30s default).
func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{http: &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Send issues the request and captures the response. A transport error (DNS, connection, timeout)
// is returned as an error; an HTTP error status is a normal Response.
func (c *Client) Send(ctx context.Context, req Request) (Response, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	hreq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return Response{}, fmt.Errorf("build request: %w", err)
	}
	for _, line := range strings.Split(req.Headers, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return Response{}, fmt.Errorf("bad header line %q (want Key: value)", line)
		}
		hreq.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
	}

	start := time.Now()
	resp, err := c.http.Do(hreq)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("read response: %w", err)
	}
	truncated := false
	if len(raw) > MaxBodyBytes {
		raw = raw[:MaxBodyBytes]
		truncated = true
	}
	out := Response{
		Status:     resp.StatusCode,
		Headers:    formatHeaders(resp.Header),
		Body:       string(raw),
		DurationMS: int(time.Since(start).Milliseconds()),
	}
	if truncated {
		out.Body += "\n\n[truncated at " + fmt.Sprint(MaxBodyBytes) + " bytes]"
	}
	return out, nil
}

func formatHeaders(h http.Header) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
