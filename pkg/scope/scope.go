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

// Dispositions (ADR-0051). Any value other than Deny is treated as an allow rule, so entries built without a
// disposition (older callers, tests) stay allow rules and behavior is unchanged.
const (
	Allow = "allow"
	Deny  = "deny"
)

// Entry is one scope rule — an in-scope allow rule or an out-of-scope (deny) exclusion.
type Entry struct {
	Kind        string
	Value       string
	Disposition string // "deny" excludes; anything else allows
}

// Check reports whether target is in scope. target may be a host, IP, or URL. Rules:
//   - A matching deny entry always blocks (out-of-scope wins over any allow).
//   - With no allow entries, scope is unconfigured → allow-all (minus denies), preserving prior behavior.
//   - With allow entries, the target must match one and not match a deny.
//
// An empty entry list means scope is entirely unconfigured and Check returns nil (allow).
func Check(entries []Entry, target string) error {
	if len(entries) == 0 {
		return nil
	}
	host := normalizeTarget(target)
	if host == "" {
		return fmt.Errorf("scope: empty target")
	}
	ip := net.ParseIP(host)

	hasAllow, matchedAllow := false, false
	for _, e := range entries {
		match := matchEntry(e, host, ip)
		if e.Disposition == Deny {
			if match {
				return fmt.Errorf("scope: target %q is out of scope (excluded)", host)
			}
			continue
		}
		hasAllow = true
		if match {
			matchedAllow = true
		}
	}
	if !hasAllow || matchedAllow {
		return nil
	}
	return fmt.Errorf("scope: target %q is not in scope", host)
}

// matchEntry reports whether an entry matches the given host (ip may be nil for non-IP hosts).
func matchEntry(e Entry, host string, ip net.IP) bool {
	switch e.Kind {
	case KindHost:
		return strings.EqualFold(host, e.Value)
	case KindDomain:
		d := strings.ToLower(strings.TrimPrefix(e.Value, "."))
		h := strings.ToLower(host)
		return h == d || strings.HasSuffix(h, "."+d)
	case KindCIDR:
		if ip != nil {
			if _, network, err := net.ParseCIDR(e.Value); err == nil {
				return network.Contains(ip)
			}
		}
	}
	return false
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
