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
	"net/url"

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

// ListContext returns a project's ingested context items.
func (c *Client) ListContext(ctx context.Context, projectID string) ([]model.ContextItem, error) {
	var out []model.ContextItem
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/context", nil, &out)
}

// IngestContext stores content as a project context item (document, email, chat, or note).
func (c *Client) IngestContext(ctx context.Context, projectID, name, ctype, mediaType string, content []byte) (model.ContextItem, error) {
	u := c.baseURL + "/v1/projects/" + projectID + "/context?name=" + url.QueryEscape(name)
	if ctype != "" {
		u += "&type=" + url.QueryEscape(ctype)
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(content))
	if err != nil {
		return model.ContextItem{}, err
	}
	req.Header.Set("Content-Type", mediaType)

	resp, err := c.http.Do(req)
	if err != nil {
		return model.ContextItem{}, err
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
		return model.ContextItem{}, fmt.Errorf("ingest context: %s", e.Error)
	}
	var out model.ContextItem
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// Search runs omni-search across projects, applications, assets, findings, and observations.
func (c *Client) Search(ctx context.Context, q string) ([]model.SearchResult, error) {
	var out []model.SearchResult
	return out, c.do(ctx, http.MethodGet, "/v1/search?q="+url.QueryEscape(q), nil, &out)
}

// SendResult is the outcome of an Analyst message or approval decision.
type SendResult struct {
	Thread      model.Thread    `json:"thread"`
	NewMessages []model.Message `json:"new_messages"`
	Answer      string          `json:"answer,omitempty"`
	Pending     *model.Approval `json:"pending_approval,omitempty"`
}

// ThreadDetail is a thread with its full message history.
type ThreadDetail struct {
	Thread   model.Thread    `json:"thread"`
	Messages []model.Message `json:"messages"`
}

// AnalystAsk creates a thread and sends one message.
func (c *Client) AnalystAsk(ctx context.Context, message string) (SendResult, error) {
	var out SendResult
	return out, c.do(ctx, http.MethodPost, "/v1/analyst/ask", map[string]string{"message": message}, &out)
}

// ListThreads returns all Analyst threads.
func (c *Client) ListThreads(ctx context.Context) ([]model.Thread, error) {
	var out []model.Thread
	return out, c.do(ctx, http.MethodGet, "/v1/threads", nil, &out)
}

// GetThread returns a thread with its messages.
func (c *Client) GetThread(ctx context.Context, id string) (ThreadDetail, error) {
	var out ThreadDetail
	return out, c.do(ctx, http.MethodGet, "/v1/threads/"+id, nil, &out)
}

// SendMessage sends a message to an existing thread.
func (c *Client) SendMessage(ctx context.Context, threadID, message string) (SendResult, error) {
	var out SendResult
	return out, c.do(ctx, http.MethodPost, "/v1/threads/"+threadID+"/messages", map[string]string{"message": message}, &out)
}

// ForkThread branches a thread at a message sequence.
func (c *Client) ForkThread(ctx context.Context, id string, seq int) (model.Thread, error) {
	var out model.Thread
	return out, c.do(ctx, http.MethodPost, "/v1/threads/"+id+"/fork", map[string]int{"seq": seq}, &out)
}

// ListApprovals returns pending approvals.
func (c *Client) ListApprovals(ctx context.Context) ([]model.Approval, error) {
	var out []model.Approval
	return out, c.do(ctx, http.MethodGet, "/v1/approvals", nil, &out)
}

// DecideApproval approves or denies an approval and resumes the run.
func (c *Client) DecideApproval(ctx context.Context, id, decision string) (SendResult, error) {
	var out SendResult
	return out, c.do(ctx, http.MethodPost, "/v1/approvals/"+id+"/decide", map[string]string{"decision": decision}, &out)
}

// Template is a project archetype reported by the control plane.
type Template struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	DefaultApplication    string   `json:"default_application"`
	SuggestedCapabilities []string `json:"suggested_capabilities"`
}

// ListTemplates returns the available project templates.
func (c *Client) ListTemplates(ctx context.Context) ([]Template, error) {
	var out []Template
	return out, c.do(ctx, http.MethodGet, "/v1/templates", nil, &out)
}

// ScaffoldResult is the outcome of creating a project from a template.
type ScaffoldResult struct {
	Project     model.Project      `json:"project"`
	Application *model.Application `json:"application,omitempty"`
	Template    Template           `json:"template"`
}

// CreateProjectFromTemplate scaffolds a project from a template.
func (c *Client) CreateProjectFromTemplate(ctx context.Context, templateID, name string) (ScaffoldResult, error) {
	var out ScaffoldResult
	body := map[string]string{"template_id": templateID, "name": name}
	return out, c.do(ctx, http.MethodPost, "/v1/projects/from-template", body, &out)
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

// CancelTask stops a running task.
func (c *Client) CancelTask(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/tasks/"+id+"/cancel", nil, nil)
}

// Playbook is a tactic (sequence of capability steps).
type Playbook struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Steps       []struct {
		Capability string `json:"capability"`
	} `json:"steps"`
}

// PlaybookRunResult is a playbook run with per-step outcomes.
type PlaybookRunResult struct {
	Run      model.PlaybookRun `json:"run"`
	Outcomes []TaskOutcome     `json:"outcomes"`
}

// ListPlaybooks returns the available playbooks.
func (c *Client) ListPlaybooks(ctx context.Context) ([]Playbook, error) {
	var out []Playbook
	return out, c.do(ctx, http.MethodGet, "/v1/playbooks", nil, &out)
}

// RunPlaybook runs a playbook against an asset.
func (c *Client) RunPlaybook(ctx context.Context, playbookID, assetID string) (PlaybookRunResult, error) {
	var out PlaybookRunResult
	return out, c.do(ctx, http.MethodPost, "/v1/playbooks/"+playbookID+"/run", map[string]string{"asset_id": assetID}, &out)
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
