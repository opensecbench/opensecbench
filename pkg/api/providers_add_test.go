package api

import (
	"net/http"
	"testing"
)

// Adding a connection when nothing is active yet should make it the active (fallback) provider, so a fresh
// setup is usable without a separate activate step. A second connection must not silently hijack the active
// one.
func TestAddProviderAutoActivatesFirstOnly(t *testing.T) {
	srv := newTestServer(t)

	var first providerView
	if code := postJSON(t, srv.URL+"/v1/analyst/providers", `{"name":"one","type":"mock"}`, &first); code != http.StatusCreated {
		t.Fatalf("add first: status %d", code)
	}
	if !first.Active {
		t.Fatalf("first provider should auto-activate: %+v", first)
	}

	var second providerView
	if code := postJSON(t, srv.URL+"/v1/analyst/providers", `{"name":"two","type":"mock"}`, &second); code != http.StatusCreated {
		t.Fatalf("add second: status %d", code)
	}
	if second.Active {
		t.Errorf("second provider should not steal active: %+v", second)
	}
}
