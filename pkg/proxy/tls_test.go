package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"testing"
	"time"
)

func selfSigned(t *testing.T, cn string, notBefore, notAfter time.Time, dns ...string) *x509.Certificate {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notBefore, NotAfter: notAfter,
		DNSNames: dns,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

func summary(t *testing.T, c *x509.Certificate, host string) CertSummary {
	t.Helper()
	var s CertSummary
	if err := json.Unmarshal([]byte(summarizeTLS(&tls.ConnectionState{PeerCertificates: []*x509.Certificate{c}}, host)), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSummarizeTLS(t *testing.T) {
	now := time.Now()

	// A valid self-signed cert: self-signed + untrusted (not a system root), but not expired / mismatched,
	// so overall not "valid".
	s := summary(t, selfSigned(t, "example.com", now.Add(-time.Hour), now.Add(time.Hour), "example.com"), "example.com:443")
	if !s.SelfSigned || !s.Untrusted {
		t.Fatalf("want self-signed + untrusted: %+v", s)
	}
	if s.Expired || s.HostnameMismatch || s.Valid {
		t.Fatalf("unexpected flags: %+v", s)
	}

	// Expired.
	s = summary(t, selfSigned(t, "old.com", now.Add(-48*time.Hour), now.Add(-24*time.Hour), "old.com"), "old.com")
	if !s.Expired {
		t.Fatalf("want expired: %+v", s)
	}

	// Hostname mismatch (cert for a.com, connecting to b.com).
	s = summary(t, selfSigned(t, "a.com", now.Add(-time.Hour), now.Add(time.Hour), "a.com"), "b.com")
	if !s.HostnameMismatch {
		t.Fatalf("want hostname mismatch: %+v", s)
	}

	// No TLS state → empty summary.
	if got := summarizeTLS(nil, "x"); got != "" {
		t.Fatalf("nil state should be empty, got %q", got)
	}
}
