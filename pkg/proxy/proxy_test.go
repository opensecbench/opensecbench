package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestCALoadOrCreatePersists(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.CertPEM()) != string(b.CertPEM()) {
		t.Fatal("CA cert not persisted across loads")
	}
	leaf, err := a.LeafFor("example.com")
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Leaf.Subject.CommonName != "example.com" {
		t.Fatalf("leaf CN = %q", leaf.Leaf.Subject.CommonName)
	}
}

type capture struct {
	mu sync.Mutex
	ex []Exchange
}

func (c *capture) add(e Exchange) { c.mu.Lock(); c.ex = append(c.ex, e); c.mu.Unlock() }

func TestProxyHTTPCapture(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "plain-ok:"+r.URL.Path)
	}))
	defer origin.Close()

	ca, _ := LoadOrCreate(t.TempDir())
	cap := &capture{}
	px := httptest.NewServer(New(ca, cap.add, nil))
	defer px.Close()

	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(mustURL(px.URL))}}
	resp, err := client.Get(origin.URL + "/hi")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "plain-ok:/hi") {
		t.Fatalf("body = %q", body)
	}
	if len(cap.ex) != 1 || !strings.Contains(cap.ex[0].ResponseBody, "plain-ok:/hi") {
		t.Fatalf("capture = %+v", cap.ex)
	}
}

func TestProxyHTTPSInterception(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Secret", "42")
		_, _ = io.WriteString(w, "tls-ok:"+r.URL.Path)
	}))
	defer origin.Close()

	ca, _ := LoadOrCreate(t.TempDir())
	cap := &capture{}
	px := httptest.NewServer(New(ca, cap.add, nil))
	defer px.Close()

	// Trust the proxy CA; the origin's real cert is never seen by the client (the proxy re-signs).
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(mustURL(px.URL)),
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}

	// The origin is an httptest TLS server on 127.0.0.1; hit it by its own URL through the proxy.
	resp, err := client.Get(origin.URL + "/secret")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "tls-ok:/secret") {
		t.Fatalf("body = %q", body)
	}
	if resp.Header.Get("X-Secret") != "42" {
		t.Fatalf("missing header from origin: %v", resp.Header)
	}
	if len(cap.ex) != 1 || !strings.Contains(cap.ex[0].URL, "/secret") || cap.ex[0].Status != 200 {
		t.Fatalf("capture = %+v", cap.ex)
	}
	if !strings.Contains(cap.ex[0].ResponseHeaders, "X-Secret: 42") {
		t.Fatalf("captured headers missing secret: %q", cap.ex[0].ResponseHeaders)
	}
}

func TestProxyScopeBlock(t *testing.T) {
	ca, _ := LoadOrCreate(t.TempDir())
	px := httptest.NewServer(New(ca, nil, func(host string) bool { return false }))
	defer px.Close()

	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(mustURL(px.URL))}}
	resp, err := client.Get("http://blocked.example/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("blocked host status = %d, want 403", resp.StatusCode)
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
