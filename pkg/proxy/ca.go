// Package proxy is the intercepting HTTP(S) proxy (ADR-0007). It forwards traffic the operator
// routes through it, capturing each request/response as an http_exchange (origin=proxy). For HTTPS
// it terminates TLS with a locally generated CA the user chooses to trust — the CA is never
// installed into any system trust store automatically.
package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CA is a locally generated certificate authority that mints short-lived per-host leaf certs for
// TLS interception. All leaves share one key; only the certificate differs per host.
type CA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte

	leafKey *ecdsa.PrivateKey

	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

// LoadOrCreate loads the CA from dir (ca.crt/ca.key) or generates and persists a new one.
func LoadOrCreate(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	crtPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	if crtPEM, err := os.ReadFile(crtPath); err == nil {
		keyPEM, kerr := os.ReadFile(keyPath)
		if kerr != nil {
			return nil, kerr
		}
		return loadCA(crtPEM, keyPEM)
	}

	ca, err := generateCA()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(crtPath, ca.certPEM, 0o600); err != nil {
		return nil, err
	}
	keyPEM, err := marshalKey(ca.key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	return ca, nil
}

func generateCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "OpenSecBench Proxy CA", Organization: []string{"OpenSecBench"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &CA{
		cert:    cert,
		key:     key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		leafKey: leafKey,
		cache:   make(map[string]*tls.Certificate),
	}, nil
}

func loadCA(crtPEM, keyPEM []byte) (*CA, error) {
	blk, _ := pem.Decode(crtPEM)
	if blk == nil {
		return nil, fmt.Errorf("proxy: bad CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, err
	}
	kblk, _ := pem.Decode(keyPEM)
	if kblk == nil {
		return nil, fmt.Errorf("proxy: bad CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(kblk.Bytes)
	if err != nil {
		return nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key, certPEM: crtPEM, leafKey: leafKey, cache: make(map[string]*tls.Certificate)}, nil
}

// CertPEM returns the CA certificate in PEM form, for the user to trust in their browser/tools.
func (c *CA) CertPEM() []byte { return c.certPEM }

// SPKISHA256 is the base64 SHA-256 of the CA's Subject Public Key Info. Chromium's
// --ignore-certificate-errors-spki-list flag takes this value to trust exactly this CA (and
// nothing else), so a launched browser can use the proxy without the CA in a system trust store.
func (c *CA) SPKISHA256() string {
	sum := sha256.Sum256(c.cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// LeafFor returns (creating and caching) a leaf certificate for the given host, signed by the CA.
func (c *CA) LeafFor(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tc, ok := c.cache[host]; ok {
		return tc, nil
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &c.leafKey.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	tc := &tls.Certificate{Certificate: [][]byte{der, c.cert.Raw}, PrivateKey: c.leafKey, Leaf: tmpl}
	c.cache[host] = tc
	return tc, nil
}

func marshalKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}
