package api

import "testing"

func TestIsSelfTraffic(t *testing.T) {
	s := &Server{selfPort: "7373"}
	cases := []struct {
		url  string
		want bool
	}{
		{"http://127.0.0.1:7373/v1/activity", true},
		{"http://localhost:7373/v1/notifications", true},
		{"http://[::1]:7373/v1/events", true},
		{"http://127.0.0.1:8080/", false}, // different port (a local target under test)
		{"https://api.example.com/v1/activity", false},
		{"http://example.com:7373/", false}, // same port but not loopback
	}
	for _, c := range cases {
		if got := s.isSelfTraffic(c.url); got != c.want {
			t.Errorf("isSelfTraffic(%q) = %v, want %v", c.url, got, c.want)
		}
	}
	// With no self port configured, nothing is filtered.
	if (&Server{}).isSelfTraffic("http://127.0.0.1:7373/") {
		t.Error("no selfPort → never self-traffic")
	}
}
