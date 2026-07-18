package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client fetches a hub's index and package archives over HTTP.
type Client struct{ http *http.Client }

// NewClient returns a hub client (timeout 0 uses 30s).
func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{http: &http.Client{Timeout: timeout}}
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

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("hub: GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func join(base, rel string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
}
