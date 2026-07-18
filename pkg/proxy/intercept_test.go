package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type fakeInterceptor struct {
	reqOn, respOn bool
	onHold        func(Held) Decision
}

func (f fakeInterceptor) Enabled() (bool, bool) { return f.reqOn, f.respOn }
func (f fakeInterceptor) Hold(_ context.Context, h Held) Decision {
	return f.onHold(h)
}

// proxyClient returns an http.Client whose plain-HTTP requests go through px.
func proxyClient(t *testing.T, px *Proxy) (*http.Client, func()) {
	t.Helper()
	psrv := httptest.NewServer(px)
	proxyURL, _ := url.Parse(psrv.URL)
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}, psrv.Close
}

func TestInterceptRequestEdit(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte("upstream-ok"))
	}))
	defer upstream.Close()

	fake := fakeInterceptor{reqOn: true, onHold: func(h Held) Decision {
		// forward, but rewrite the request body (a different length than the original)
		return Decision{Method: h.Method, URL: h.URL, RequestHeaders: h.RequestHeaders, RequestBody: "EDITED-LONGER-BODY"}
	}}
	px := New(nil, nil, nil, fake)
	client, closeProxy := proxyClient(t, px)
	defer closeProxy()

	resp, err := client.Post(upstream.URL+"/x", "text/plain", strings.NewReader("orig"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if gotBody != "EDITED-LONGER-BODY" {
		t.Fatalf("upstream received %q, want the edited body", gotBody)
	}
	if string(body) != "upstream-ok" {
		t.Fatalf("client got %q, want upstream-ok", body)
	}
}

func TestInterceptRequestDrop(t *testing.T) {
	hit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hit = true }))
	defer upstream.Close()

	fake := fakeInterceptor{reqOn: true, onHold: func(Held) Decision { return Decision{Drop: true} }}
	client, closeProxy := proxyClient(t, New(nil, nil, nil, fake))
	defer closeProxy()

	resp, err := client.Get(upstream.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("dropped request returned %d, want 403", resp.StatusCode)
	}
	if hit {
		t.Fatal("upstream was reached despite a dropped request")
	}
}

func TestInterceptResponseEdit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("original-response"))
	}))
	defer upstream.Close()

	fake := fakeInterceptor{respOn: true, onHold: func(h Held) Decision {
		return Decision{Status: 418, ResponseHeaders: h.ResponseHeaders, ResponseBody: "REWRITTEN"}
	}}
	var captured Exchange
	px := New(nil, func(e Exchange) { captured = e }, nil, fake)
	client, closeProxy := proxyClient(t, px)
	defer closeProxy()

	resp, err := client.Get(upstream.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 418 {
		t.Fatalf("client got status %d, want the edited 418", resp.StatusCode)
	}
	if string(body) != "REWRITTEN" {
		t.Fatalf("client got body %q, want REWRITTEN", body)
	}
	// Capture reflects what was actually delivered.
	if captured.Status != 418 || captured.ResponseBody != "REWRITTEN" {
		t.Fatalf("captured exchange = %+v, want the delivered 418/REWRITTEN", captured)
	}
}
