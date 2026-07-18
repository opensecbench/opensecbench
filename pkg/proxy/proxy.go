package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
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
	transport  *http.Transport
}

// New builds a proxy. onExchange persists captures; allow gates hosts (nil allows all).
func New(ca *CA, onExchange func(Exchange), allow func(host string) bool) *Proxy {
	if onExchange == nil {
		onExchange = func(Exchange) {}
	}
	if allow == nil {
		allow = func(string) bool { return true }
	}
	return &Proxy{
		ca:         ca,
		onExchange: onExchange,
		allow:      allow,
		transport: &http.Transport{
			Proxy:               nil,
			TLSHandshakeTimeout: 10 * time.Second,
			// Assessment targets routinely present self-signed or otherwise invalid certs; an
			// intercepting proxy must still reach them (Burp behaves the same). Upstream cert
			// validity is not enforced here.
			// TODO(P7+): capture the upstream cert chain + validity onto the exchange so an invalid
			// origin cert is surfaced rather than silently accepted.
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // see above
			ResponseHeaderTimeout: 60 * time.Second,
		},
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

// handleHTTP forwards a plain-HTTP proxy request (absolute-URI) and captures it.
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.allow(hostname(r.URL.Host)) {
		http.Error(w, "blocked by scope guard: "+r.URL.Host, http.StatusForbidden)
		return
	}
	reqBody, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	outReq, err := http.NewRequest(r.Method, r.URL.String(), strings.NewReader(string(reqBody)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	copyHeaders(outReq.Header, r.Header)

	start := time.Now()
	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	ex := Exchange{
		Method: r.Method, URL: r.URL.String(),
		RequestHeaders: formatHeaders(r.Header), RequestBody: capString(reqBody),
		Status: resp.StatusCode, ResponseHeaders: formatHeaders(resp.Header),
	}

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	cw := &capWriter{max: MaxCaptureBytes}
	_, _ = io.Copy(w, io.TeeReader(resp.Body, cw))
	ex.ResponseBody = string(cw.buf)
	ex.DurationMS = int(time.Since(start).Milliseconds())
	p.onExchange(ex)
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

	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return // client closed or malformed; end the tunnel
		}
		if !p.forwardTLS(tlsConn, req, host, r.Host) {
			return
		}
	}
}

// forwardTLS sends one decrypted request to the real origin over TLS, writes the response back to
// the client, and captures the exchange. It returns false when the connection should close.
func (p *Proxy) forwardTLS(clientConn net.Conn, req *http.Request, host, authority string) bool {
	reqBody, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()

	url := "https://" + authority + req.URL.RequestURI()
	outReq, err := http.NewRequest(req.Method, url, strings.NewReader(string(reqBody)))
	if err != nil {
		return false
	}
	copyHeaders(outReq.Header, req.Header)

	start := time.Now()
	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		_, _ = fmt.Fprintf(clientConn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return false
	}

	ex := Exchange{
		Method: req.Method, URL: url,
		RequestHeaders: formatHeaders(req.Header), RequestBody: capString(reqBody),
		Status: resp.StatusCode, ResponseHeaders: formatHeaders(resp.Header),
	}
	cw := &capWriter{max: MaxCaptureBytes}
	resp.Body = readCloser{io.TeeReader(resp.Body, cw), resp.Body}
	writeErr := resp.Write(clientConn)
	_ = resp.Body.Close()
	ex.ResponseBody = string(cw.buf)
	ex.DurationMS = int(time.Since(start).Milliseconds())
	p.onExchange(ex)

	// Honor connection close semantics.
	return writeErr == nil && !req.Close && strings.ToLower(req.Header.Get("Connection")) != "close"
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
