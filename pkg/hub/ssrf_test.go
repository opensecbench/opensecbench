package hub

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestBlockedIP(t *testing.T) {
	c := &Client{} // allowLoopback = false
	blocked := []string{"127.0.0.1", "::1", "169.254.169.254", "0.0.0.0", "224.0.0.1", "fe80::1"}
	for _, s := range blocked {
		if !c.blockedIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be blocked", s)
		}
	}
	// RFC1918 private ranges are allowed (enterprises self-host internal hubs), as are public addresses.
	for _, s := range []string{"10.0.0.5", "192.168.1.10", "172.16.0.1", "8.8.8.8"} {
		if c.blockedIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be allowed", s)
		}
	}
	// The test escape hatch permits loopback.
	if (&Client{allowLoopback: true}).blockedIP(net.ParseIP("127.0.0.1")) {
		t.Error("allowLoopback should permit loopback")
	}
}

func TestFetchRejectsNonHTTPScheme(t *testing.T) {
	_, err := NewClient(0).FetchIndex(context.Background(), "file:///etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("expected scheme rejection, got %v", err)
	}
}

func TestFetchRejectsMetadataAddress(t *testing.T) {
	// 169.254.169.254 is link-local (cloud metadata) — the dial guard must refuse it.
	_, err := NewClient(0).FetchIndex(context.Background(), "http://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Fatal("expected the metadata address to be refused")
	}
}
