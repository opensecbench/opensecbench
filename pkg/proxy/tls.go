package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"strings"
	"time"
)

// CertSummary is what the proxy records about an upstream server's TLS certificate (review #6). The proxy
// intentionally forwards to targets with invalid certs (InsecureSkipVerify) — assessment targets routinely
// present self-signed or expired certs — but instead of silently accepting them it captures the cert and
// flags the problems onto the exchange, so an operator sees them.
type CertSummary struct {
	Subject          string `json:"subject"`
	Issuer           string `json:"issuer"`
	NotBefore        string `json:"not_before"`
	NotAfter         string `json:"not_after"`
	Expired          bool   `json:"expired,omitempty"`
	HostnameMismatch bool   `json:"hostname_mismatch,omitempty"`
	SelfSigned       bool   `json:"self_signed,omitempty"`
	Untrusted        bool   `json:"untrusted,omitempty"` // chain doesn't verify to a system root (self-signed or unknown CA)
	Valid            bool   `json:"valid"`               // none of the above problems
}

// summarizeTLS evaluates the presented server certificate for host and returns a JSON CertSummary, or "" when
// there is no TLS state / no certificate (plain HTTP). host may include a port, which is stripped.
func summarizeTLS(state *tls.ConnectionState, host string) string {
	if state == nil || len(state.PeerCertificates) == 0 {
		return ""
	}
	leaf := state.PeerCertificates[0]
	now := time.Now()
	host = strings.TrimSuffix(host, ".")
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}

	s := CertSummary{
		Subject:   nameOf(leaf.Subject.CommonName, leaf.DNSNames),
		Issuer:    leaf.Issuer.CommonName,
		NotBefore: leaf.NotBefore.UTC().Format(time.RFC3339),
		NotAfter:  leaf.NotAfter.UTC().Format(time.RFC3339),
	}
	s.Expired = now.Before(leaf.NotBefore) || now.After(leaf.NotAfter)
	s.HostnameMismatch = leaf.VerifyHostname(host) != nil
	s.SelfSigned = len(state.PeerCertificates) == 1 && leaf.Issuer.String() == leaf.Subject.String()

	// Verify the presented chain against the system trust store (independent of the proxy's own MITM CA).
	inter := x509.NewCertPool()
	for _, c := range state.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Intermediates: inter, CurrentTime: now}); err != nil {
		s.Untrusted = true
	}

	s.Valid = !s.Expired && !s.HostnameMismatch && !s.Untrusted
	b, _ := json.Marshal(s)
	return string(b)
}

// nameOf prefers the cert CN, falling back to the first SAN when the CN is empty.
func nameOf(cn string, sans []string) string {
	if cn != "" {
		return cn
	}
	if len(sans) > 0 {
		return sans[0]
	}
	return ""
}
