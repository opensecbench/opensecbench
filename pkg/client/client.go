// Package client is a Go client for the control-plane HTTP API. Clients (the CLI today, other
// tooling later) talk to the control plane only through this boundary (ADR-0001).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// Client calls the OpenSecBench control-plane API.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a client for the control plane at baseURL (e.g. "http://127.0.0.1:7373").
func New(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: http.DefaultClient}
}

// Health returns the control plane's health payload.
func (c *Client) Health(ctx context.Context) (map[string]string, error) {
	var out map[string]string
	return out, c.do(ctx, http.MethodGet, "/healthz", nil, &out)
}

// ListProjects returns all projects.
func (c *Client) ListProjects(ctx context.Context) ([]model.Project, error) {
	var out []model.Project
	return out, c.do(ctx, http.MethodGet, "/v1/projects", nil, &out)
}

// GetProject returns a single project by id.
func (c *Client) GetProject(ctx context.Context, id string) (model.Project, error) {
	var out model.Project
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+id, nil, &out)
}

// CreateProjectRequest is the payload for creating a project.
type CreateProjectRequest struct {
	Name      string   `json:"name"`
	TargetIDs []string `json:"target_ids,omitempty"`
}

// CreateProject creates a project.
func (c *Client) CreateProject(ctx context.Context, req CreateProjectRequest) (model.Project, error) {
	var out model.Project
	return out, c.do(ctx, http.MethodPost, "/v1/projects", req, &out)
}

// DeleteProject deletes a project by id.
func (c *Client) DeleteProject(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/projects/"+id, nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("%s %s: %s", method, path, e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
