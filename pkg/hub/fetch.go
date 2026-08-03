package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxResponseBytes caps a hub response so a hostile endpoint can't exhaust memory. Extensions are small.
const maxResponseBytes = 64 << 20 // 64 MiB

// Client fetches a hub's index and package archives over HTTP. The URL is caller-supplied, so the client
// is SSRF-guarded: only http/https, redirects are re-checked, and connections to loopback/link-local
// (cloud metadata) targets are refused at dial time — after DNS resolution, so a hostname can't rebind
// past the check. RFC1918 private ranges are allowed: enterprises self-host hubs on internal networks.
type Client struct {
	http          *http.Client
	allowLoopback bool // test-only: httptest servers listen on loopback
}

// NewClient returns a hub client (timeout 0 uses 30s).
func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	c := &Client{}
	c.http = &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: c.guardedDial},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("hub: too many redirects")
			}
			return validateScheme(req.URL)
		},
	}
	return c
}

// guardedDial resolves the target and refuses a non-public address before connecting, then dials the exact
// IP it validated (closing the DNS-rebinding window). Applies to the initial request and every redirect.
func (c *Client) guardedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if c.blockedIP(ip.IP) {
			return nil, fmt.Errorf("hub: refusing to connect to disallowed address %s", ip.IP)
		}
	}
	var d net.Dialer
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

// blockedIP reports whether an address is off-limits: loopback (pivot to local services) and link-local
// (169.254.169.254 cloud metadata), plus unspecified/multicast. RFC1918 private ranges stay allowed.
func (c *Client) blockedIP(ip net.IP) bool {
	if ip.IsLoopback() {
		return !c.allowLoopback
	}
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func validateScheme(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("hub: unsupported URL scheme %q (only http/https)", u.Scheme)
	}
	return nil
}

// FetchIndex retrieves and parses a hub's index.json from baseURL.
func (c *Client) FetchIndex(ctx context.Context, baseURL string) (Index, error) {
	body, err := c.get(ctx, join(baseURL, IndexFile))
	if err != nil {
		return Index{}, err
	}
	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return Index{}, fmt.Errorf("hub: bad index: %w", err)
	}
	return idx, nil
}

// DownloadArchive fetches an entry's archive from baseURL and verifies its digest.
func (c *Client) DownloadArchive(ctx context.Context, baseURL string, e PackageEntry) ([]byte, error) {
	data, err := c.get(ctx, join(baseURL, e.Archive))
	if err != nil {
		return nil, err
	}
	if !VerifyDigest(data, e.Digest) {
		return nil, fmt.Errorf("hub: archive digest mismatch for %s (corrupt or tampered)", e.ID)
	}
	return data, nil
}

func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if err := validateScheme(req.URL); err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("hub: GET %s: %s", rawURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("hub: response exceeds %d bytes", maxResponseBytes)
	}
	return body, nil
}

func join(base, rel string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
}
