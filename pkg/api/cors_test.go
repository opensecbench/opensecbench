package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCORSAllowsProjectHeader guards the CORS preflight: the X-Project-Id header the frontend attaches to
// scope requests to the active project (ADR-0049) must be listed in Access-Control-Allow-Headers, or the
// browser blocks every project-scoped request ("Load failed" on each tab).
func TestCORSAllowsProjectHeader(t *testing.T) {
	srv := httptest.NewServer(New(Deps{}).Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/v1/projects/x/observations", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "x-project-id")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	got := resp.Header.Get("Access-Control-Allow-Headers")
	if got != "Content-Type, X-Project-Id" {
		t.Fatalf("Access-Control-Allow-Headers = %q, want it to include X-Project-Id", got)
	}
}
