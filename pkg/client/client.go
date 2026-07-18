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

// ListApplications returns a project's applications.
func (c *Client) ListApplications(ctx context.Context, projectID string) ([]model.Application, error) {
	var out []model.Application
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/applications", nil, &out)
}

// CreateApplication creates an application under a project.
func (c *Client) CreateApplication(ctx context.Context, projectID, name string) (model.Application, error) {
	var out model.Application
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/applications", map[string]string{"name": name}, &out)
}

// ListAssets returns an application's assets.
func (c *Client) ListAssets(ctx context.Context, applicationID string) ([]model.Asset, error) {
	var out []model.Asset
	return out, c.do(ctx, http.MethodGet, "/v1/applications/"+applicationID+"/assets", nil, &out)
}

// CreateAssetRequest is the payload for creating an asset (sensitivity may be empty to infer).
type CreateAssetRequest struct {
	Type        string `json:"type"`
	Location    string `json:"location"`
	Sensitivity string `json:"sensitivity,omitempty"`
}

// CreateAsset creates an asset under an application.
func (c *Client) CreateAsset(ctx context.Context, applicationID string, req CreateAssetRequest) (model.Asset, error) {
	var out model.Asset
	return out, c.do(ctx, http.MethodPost, "/v1/applications/"+applicationID+"/assets", req, &out)
}

// CapabilityManifest is a capability as reported by the control plane.
type CapabilityManifest struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ListCapabilities returns the available capabilities.
func (c *Client) ListCapabilities(ctx context.Context) ([]CapabilityManifest, error) {
	var out []CapabilityManifest
	return out, c.do(ctx, http.MethodGet, "/v1/capabilities", nil, &out)
}

// RunTaskRequest asks the control plane to run a capability against a target directory or asset.
type RunTaskRequest struct {
	CapabilityID string         `json:"capability_id"`
	TargetDir    string         `json:"target_dir,omitempty"`
	AssetID      *string        `json:"asset_id,omitempty"`
	Actor        string         `json:"actor,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
}

// TaskOutcome is a completed task with its artifacts and interpreted observations.
type TaskOutcome struct {
	Task         model.Task          `json:"task"`
	Artifacts    []model.Artifact    `json:"artifacts"`
	Observations []model.Observation `json:"observations"`
}

// RunTask runs a capability and returns the resulting task and artifacts.
func (c *Client) RunTask(ctx context.Context, req RunTaskRequest) (TaskOutcome, error) {
	var out TaskOutcome
	return out, c.do(ctx, http.MethodPost, "/v1/tasks", req, &out)
}

// GetTask returns a task by id.
func (c *Client) GetTask(ctx context.Context, id string) (model.Task, error) {
	var out model.Task
	return out, c.do(ctx, http.MethodGet, "/v1/tasks/"+id, nil, &out)
}

// ListTaskObservations returns the observations interpreted from a task's outputs.
func (c *Client) ListTaskObservations(ctx context.Context, taskID string) ([]model.Observation, error) {
	var out []model.Observation
	return out, c.do(ctx, http.MethodGet, "/v1/tasks/"+taskID+"/observations", nil, &out)
}

// ReviewObservation sets an observation's review state (confirmed | rejected | unreviewed).
func (c *Client) ReviewObservation(ctx context.Context, id, state string) error {
	return c.do(ctx, http.MethodPost, "/v1/observations/"+id+"/review", map[string]string{"state": state}, nil)
}

// CreateFindingRequest assembles a finding from confirmed observations.
type CreateFindingRequest struct {
	Title          string   `json:"title"`
	Severity       string   `json:"severity,omitempty"`
	Description    string   `json:"description,omitempty"`
	CWE            string   `json:"cwe,omitempty"`
	ObservationIDs []string `json:"observation_ids,omitempty"`
}

// CreateFinding creates a finding.
func (c *Client) CreateFinding(ctx context.Context, req CreateFindingRequest) (model.Finding, error) {
	var out model.Finding
	return out, c.do(ctx, http.MethodPost, "/v1/findings", req, &out)
}

// ListFindings returns all findings.
func (c *Client) ListFindings(ctx context.Context) ([]model.Finding, error) {
	var out []model.Finding
	return out, c.do(ctx, http.MethodGet, "/v1/findings", nil, &out)
}

// GetFinding returns a finding by id.
func (c *Client) GetFinding(ctx context.Context, id string) (model.Finding, error) {
	var out model.Finding
	return out, c.do(ctx, http.MethodGet, "/v1/findings/"+id, nil, &out)
}

// ArtifactContent fetches an artifact's raw bytes from the CAS.
func (c *Client) ArtifactContent(ctx context.Context, id string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/artifacts/"+id+"/content", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("GET artifact content: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
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
