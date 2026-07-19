package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MaxCaptureBytes caps how much of a request/response body is stored on the exchange. The client
// still receives the full body; only the captured copy is bounded.
const MaxCaptureBytes = 2 << 20 // 2 MiB

// Exchange is a captured request/response pair produced by the proxy.
type Exchange struct {
	Method          string
	URL             string
	RequestHeaders  string
	RequestBody     string
	Status          int
	ResponseHeaders string
	ResponseBody    string
	DurationMS      int
}

// Proxy is an intercepting HTTP(S) proxy. Traffic routed through it is forwarded and captured via
// the OnExchange hook; the Allow hook (scope guard) can refuse a host before any bytes are sent.
type Proxy struct {
	ca         *CA
	onExchange func(Exchange)
	allow      func(host string) bool
	intercept  Interceptor
	process    Processor
	transport  http.RoundTripper // opens the upstream socket; may be a runner-routed forwarder (ADR-0026)
}

// New builds a proxy. onExchange persists captures; allow gates hosts (nil allows all); intercept
// holds traffic for operator edit/forward/drop (nil disables interception); process applies automatic
// match/replace-style transforms (nil = no transforms). forward, when non-nil, replaces the local upstream
// transport — used to route every forward through a remote runner's vantage (ADR-0026).
func New(ca *CA, onExchange func(Exchange), allow func(host string) bool, intercept Interceptor, process Processor, forward http.RoundTripper) *Proxy {
	if onExchange == nil {
		onExchange = func(Exchange) {}
	}
	if allow == nil {
		allow = func(string) bool { return true }
	}
	if intercept == nil {
		intercept = noopInterceptor{}
	}
	if process == nil {
		process = noopProcessor{}
	}
	if forward == nil {
		forward = &http.Transport{
			Proxy:               nil,
			TLSHandshakeTimeout: 10 * time.Second,
			// Assessment targets routinely present self-signed or otherwise invalid certs; an
			// intercepting proxy must still reach them (Burp behaves the same). Upstream cert
			// validity is not enforced here.
			// TODO(P7+): capture the upstream cert chain + validity onto the exchange so an invalid
			// origin cert is surfaced rather than silently accepted.
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // see above
			ResponseHeaderTimeout: 60 * time.Second,
		}
	}
	return &Proxy{
		ca:         ca,
		onExchange: onExchange,
		allow:      allow,
		intercept:  intercept,
		process:    process,
		transport:  forward,
	}
}

// CACertPEM exposes the CA certificate for the operator to trust.
func (p *Proxy) CACertPEM() []byte { return p.ca.CertPEM() }

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// handleHTTP forwards a plain-HTTP proxy request (absolute-URI), applying interception at the request
// and response choke points, and captures what was actually delivered.
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.allow(hostname(r.URL.Host)) {
		http.Error(w, "blocked by scope guard: "+r.URL.Host, http.StatusForbidden)
		return
	}
	reqBody, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	ctx := r.Context()

	reqOn, respOn := p.intercept.Enabled()
	method, url, header, body := r.Method, r.URL.String(), r.Header, reqBody
	method, url, header, body = p.applyRequestRules(method, url, header, body) // auto match/replace
	if reqOn {
		var dropped bool
		if method, url, header, body, dropped = p.holdRequest(ctx, method, url, header, body); dropped {
			http.Error(w, "request dropped by operator", http.StatusForbidden)
			return
		}
	}

	outReq, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	copyHeaders(outReq.Header, header)
	fixRequestFraming(outReq) // an edited body may have changed the length

	start := time.Now()
	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Buffer when a rule or the operator needs the full response body.
	if respOn || p.process.NeedsResponseBody() {
		respBody, _ := io.ReadAll(resp.Body)
		status, respHeader, outBody := p.applyResponseRules(resp.StatusCode, resp.Header, respBody) // auto
		if respOn {
			var dropped bool
			if status, respHeader, outBody, dropped = p.holdResponse(ctx, method, url, status, respHeader, outBody); dropped {
				http.Error(w, "response dropped by operator", http.StatusForbidden)
				p.capture(method, url, header, body, resp.StatusCode, resp.Header, respBody, start)
				return
			}
		}
		writeBufferedResponse(w, status, respHeader, outBody)
		p.capture(method, url, header, body, status, respHeader, outBody, start)
		return
	}

	// Otherwise stream through, capturing a bounded copy.
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	cw := &capWriter{max: MaxCaptureBytes}
	_, _ = io.Copy(w, io.TeeReader(resp.Body, cw))
	p.capture(method, url, header, body, resp.StatusCode, resp.Header, cw.buf, start)
}

// writeBufferedResponse writes an edited, fully-buffered response, fixing framing headers.
func writeBufferedResponse(w http.ResponseWriter, status int, header http.Header, body []byte) {
	copyHeaders(w.Header(), header)
	w.Header().Del("Transfer-Encoding")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// handleConnect terminates TLS with a per-host leaf cert and proxies the decrypted requests,
// capturing each. This is the HTTPS interception path.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := hostname(r.Host)
	if !p.allow(host) {
		http.Error(w, "blocked by scope guard: "+host, http.StatusForbidden)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = clientConn.Close() }()

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	leaf, err := p.ca.LeafFor(host)
	if err != nil {
		return
	}
	tlsConn := tls.Server(clientConn, &tls.Config{Certificates: []tls.Certificate{*leaf}})
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	defer func() { _ = tlsConn.Close() }()

	// Requests read off a hijacked tunnel carry no server context, so derive one that cancels when
	// the tunnel ends — held requests on this connection then auto-drop.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return // client closed or malformed; end the tunnel
		}
		if !p.forwardTLS(ctx, tlsConn, req, host, r.Host) {
			return
		}
	}
}

// forwardTLS sends one decrypted request to the real origin over TLS, writes the response back to
// the client, and captures the exchange. It returns false when the connection should close.
func (p *Proxy) forwardTLS(ctx context.Context, clientConn net.Conn, req *http.Request, host, authority string) bool {
	reqBody, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()

	keepOpen := !req.Close && strings.ToLower(req.Header.Get("Connection")) != "close"
	reqOn, respOn := p.intercept.Enabled()
	method, url, header, body := req.Method, "https://"+authority+req.URL.RequestURI(), req.Header, reqBody
	method, url, header, body = p.applyRequestRules(method, url, header, body) // auto match/replace
	if reqOn {
		var dropped bool
		if method, url, header, body, dropped = p.holdRequest(ctx, method, url, header, body); dropped {
			_, _ = fmt.Fprintf(clientConn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
			return keepOpen
		}
	}

	outReq, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return false
	}
	copyHeaders(outReq.Header, header)
	fixRequestFraming(outReq)

	start := time.Now()
	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		_, _ = fmt.Fprintf(clientConn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return false
	}

	if respOn || p.process.NeedsResponseBody() {
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		status, respHeader, outBody := p.applyResponseRules(resp.StatusCode, resp.Header, respBody) // auto
		if respOn {
			var dropped bool
			if status, respHeader, outBody, dropped = p.holdResponse(ctx, method, url, status, respHeader, outBody); dropped {
				_, _ = fmt.Fprintf(clientConn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
				p.capture(method, url, header, body, resp.StatusCode, resp.Header, respBody, start)
				return keepOpen
			}
		}
		respHeader.Del("Transfer-Encoding")
		respHeader.Set("Content-Length", strconv.Itoa(len(outBody)))
		outResp := &http.Response{
			StatusCode: status, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header: respHeader, Body: io.NopCloser(bytes.NewReader(outBody)),
			ContentLength: int64(len(outBody)), Request: outReq,
		}
		writeErr := outResp.Write(clientConn)
		p.capture(method, url, header, body, status, respHeader, outBody, start)
		return writeErr == nil && keepOpen
	}

	cw := &capWriter{max: MaxCaptureBytes}
	resp.Body = readCloser{io.TeeReader(resp.Body, cw), resp.Body}
	writeErr := resp.Write(clientConn)
	_ = resp.Body.Close()
	p.capture(method, url, header, body, resp.StatusCode, resp.Header, cw.buf, start)
	return writeErr == nil && keepOpen
}

type readCloser struct {
	io.Reader
	io.Closer
}

// capWriter captures up to max bytes and silently drops the rest (reporting full writes so a
// TeeReader keeps streaming to the real destination).
type capWriter struct {
	buf []byte
	max int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if room := c.max - len(c.buf); room > 0 {
		if len(p) > room {
			c.buf = append(c.buf, p[:room]...)
		} else {
			c.buf = append(c.buf, p...)
		}
	}
	return len(p), nil
}

func capString(b []byte) string {
	if len(b) > MaxCaptureBytes {
		b = b[:MaxCaptureBytes]
	}
	return string(b)
}

func hostname(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if k == "Proxy-Connection" {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
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
