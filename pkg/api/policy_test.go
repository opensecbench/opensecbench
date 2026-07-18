package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/policy"
)

func TestPolicyProfileFlow(t *testing.T) {
	srv := newTestServer(t)

	var profiles []policy.Profile
	postGet(t, srv.URL+"/v1/policy/profiles", &profiles)
	if len(profiles) != 3 {
		t.Fatalf("profiles = %d, want 3", len(profiles))
	}

	// Default active profile is the conservative one.
	var active policy.Profile
	postGet(t, srv.URL+"/v1/policy/active", &active)
	if active.Name != policy.Default || active.AllowExternalForPrivate {
		t.Fatalf("default active = %+v, want %s (no external-for-private)", active, policy.Default)
	}

	// Switch to personal (permissive) via PUT.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/policy/active", strings.NewReader(`{"profile":"personal"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set profile = %d", resp.StatusCode)
	}
	postGet(t, srv.URL+"/v1/policy/active", &active)
	if active.Name != "personal" || !active.AllowExternalForPrivate {
		t.Fatalf("active after set = %+v, want personal (external allowed)", active)
	}

	// Unknown profile rejected.
	req2, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/policy/active", strings.NewReader(`{"profile":"bogus"}`))
	resp2, _ := http.DefaultClient.Do(req2)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown profile = %d, want 400", resp2.StatusCode)
	}
}
