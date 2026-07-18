// Package scope enforces the in-scope target allowlist (ADR-0001, P6). A capability that touches
// a network target must have that target inside the engagement's scope, or it is blocked.
package scope

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Entry kinds.
const (
	KindHost   = "host"   // exact host or IP, e.g. api.acme.com or 10.0.0.5
	KindDomain = "domain" // a domain and its subdomains, e.g. acme.com matches api.acme.com
	KindCIDR   = "cidr"   // an IP range, e.g. 10.0.0.0/24
)

// Entry is one in-scope allowlist rule.
type Entry struct {
	Kind  string
	Value string
}

// Check reports whether target is in scope. target may be a host, IP, or URL. An empty entry list
// means scope is unconfigured and Check returns nil (allow) — enforcement is the caller's choice.
func Check(entries []Entry, target string) error {
	if len(entries) == 0 {
		return nil
	}
	host := normalizeTarget(target)
	if host == "" {
		return fmt.Errorf("scope: empty target")
	}
	ip := net.ParseIP(host)

	for _, e := range entries {
		switch e.Kind {
		case KindHost:
			if strings.EqualFold(host, e.Value) {
				return nil
			}
		case KindDomain:
			d := strings.ToLower(strings.TrimPrefix(e.Value, "."))
			h := strings.ToLower(host)
			if h == d || strings.HasSuffix(h, "."+d) {
				return nil
			}
		case KindCIDR:
			if ip != nil {
				if _, network, err := net.ParseCIDR(e.Value); err == nil && network.Contains(ip) {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("scope: target %q is not in scope", host)
}

// normalizeTarget extracts a bare host from a host, IP, or URL.
func normalizeTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if strings.Contains(target, "://") {
		if u, err := url.Parse(target); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	// Strip a port if present (host:port), but not for bare IPv6.
	if h, _, err := net.SplitHostPort(target); err == nil {
		return h
	}
	return target
}
