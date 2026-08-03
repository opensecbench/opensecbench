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
	"strconv"

	"github.com/opensecbench/opensecbench/pkg/store"
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// Client calls the OpenSecBench control-plane API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// Option configures a Client at construction.
type Option func(*Client)

// WithToken makes the client present tok as a bearer credential on every request (ADR-0061). The
// token is the daemon's local API token, read from controlplane.APITokenPath. An empty token is a
// no-op, so an unauthenticated daemon (older builds, tests) still works.
func WithToken(tok string) Option { return func(c *Client) { c.token = tok } }

// New returns a client for the control plane at baseURL (e.g. "http://127.0.0.1:7373"). The returned
// client has no request timeout: it must support long-lived Server-Sent Events streams (see Attach).
func New(baseURL string, opts ...Option) *Client {
	c := &Client{baseURL: baseURL}
	for _, o := range opts {
		o(c)
	}
	// A transport wrapper attaches the bearer token to every request — do(), the direct-request
	// helpers, and the Attach stream alike — so authentication can never be forgotten on a new path.
	c.http = &http.Client{Transport: authTransport{token: c.token, base: http.DefaultTransport}}
	return c
}

// authTransport adds the ADR-0061 bearer token to every outgoing request. It clones the request before
// mutating headers, per the http.RoundTripper contract, and no-ops when no token is configured.
type authTransport struct {
	token string
	base  http.RoundTripper
}

func (t authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.token != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(r)
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
	// Location, when set, is a directory to keep this project's files in (project.db + cas + workspace),
	// instead of the default data dir (ADR-0049). The TUI uses <cwd>/.opensecbench for a dir-local project.
	Location string `json:"location,omitempty"`
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

// ListScope returns a project's in-scope allowlist entries.
func (c *Client) ListScope(ctx context.Context, projectID string) ([]model.ScopeEntry, error) {
	var out []model.ScopeEntry
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/scope", nil, &out)
}

// AddScope adds an in-scope allowlist entry (kind: host | domain | cidr) to a project.
func (c *Client) AddScope(ctx context.Context, projectID, kind, value string) (model.ScopeEntry, error) {
	var out model.ScopeEntry
	body := map[string]string{"kind": kind, "value": value}
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/scope", body, &out)
}

// DeleteScope removes an in-scope allowlist entry.
func (c *Client) DeleteScope(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/scope/"+id, nil, nil)
}

// ListExchanges returns a project's HTTP exchanges (Replay), newest first.
func (c *Client) ListExchanges(ctx context.Context, projectID string) ([]model.HTTPExchange, error) {
	var out []model.HTTPExchange
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/exchanges", nil, &out)
}

// NewExchange is a draft HTTP request to create in the Replay.
type NewExchange struct {
	Name           string `json:"name,omitempty"`
	Method         string `json:"method,omitempty"`
	URL            string `json:"url"`
	RequestHeaders string `json:"request_headers,omitempty"`
	RequestBody    string `json:"request_body,omitempty"`
}

// CreateExchange records a draft request.
func (c *Client) CreateExchange(ctx context.Context, projectID string, req NewExchange) (model.HTTPExchange, error) {
	var out model.HTTPExchange
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/exchanges", req, &out)
}

// GetExchange returns an exchange by id.
func (c *Client) GetExchange(ctx context.Context, id string) (model.HTTPExchange, error) {
	var out model.HTTPExchange
	return out, c.do(ctx, http.MethodGet, "/v1/exchanges/"+id, nil, &out)
}

// SendExchange scope-guards and issues the request, returning the exchange with its response.
func (c *Client) SendExchange(ctx context.Context, id string) (model.HTTPExchange, error) {
	var out model.HTTPExchange
	return out, c.do(ctx, http.MethodPost, "/v1/exchanges/"+id+"/send", nil, &out)
}

// SaveExchangeEvidence promotes a sent response into an observation (evidence).
func (c *Client) SaveExchangeEvidence(ctx context.Context, id, note string) (model.Observation, error) {
	var out model.Observation
	return out, c.do(ctx, http.MethodPost, "/v1/exchanges/"+id+"/evidence", map[string]string{"note": note}, &out)
}

// ProxyStatus reports whether a project's intercepting proxy is running and on which port.
type ProxyStatus struct {
	Running bool   `json:"running"`
	Port    int    `json:"port,omitempty"`
	CASPKI  string `json:"ca_spki_sha256,omitempty"` // base64 SHA-256 of the CA SPKI (for browser trust)
}

// GetProxy returns a project's proxy status.
func (c *Client) GetProxy(ctx context.Context, projectID string) (ProxyStatus, error) {
	var out ProxyStatus
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/proxy", nil, &out)
}

// StartProxy starts the intercepting proxy for a project (port 0 auto-assigns).
func (c *Client) StartProxy(ctx context.Context, projectID string, port int) (ProxyStatus, error) {
	var out ProxyStatus
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/proxy/start", map[string]int{"port": port}, &out)
}

// StopProxy stops a project's intercepting proxy.
func (c *Client) StopProxy(ctx context.Context, projectID string) (ProxyStatus, error) {
	var out ProxyStatus
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/proxy/stop", nil, &out)
}

// ProxyCACert fetches the proxy CA certificate (PEM) for the operator to trust.
func (c *Client) ProxyCACert(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/proxy/ca", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("proxy ca: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// NotificationFeed is the notifications list plus the unread count.
type NotificationFeed struct {
	Unread        int                  `json:"unread"`
	Notifications []model.Notification `json:"notifications"`
}

// ListNotifications returns notifications (newest first) and the unread count.
func (c *Client) ListNotifications(ctx context.Context, unreadOnly bool, limit int) (NotificationFeed, error) {
	path := "/v1/notifications?limit=" + strconv.Itoa(limit)
	if unreadOnly {
		path += "&unread=true"
	}
	var out NotificationFeed
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// MarkNotificationRead marks one notification read.
func (c *Client) MarkNotificationRead(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/notifications/"+id+"/read", nil, nil)
}

// MarkAllNotificationsRead marks all notifications read.
func (c *Client) MarkAllNotificationsRead(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/notifications/read-all", nil, nil)
}

// ListSecrets returns vault secret metadata (names only).
func (c *Client) ListSecrets(ctx context.Context) ([]model.Secret, error) {
	var out []model.Secret
	return out, c.do(ctx, http.MethodGet, "/v1/secrets", nil, &out)
}

// SetSecret seals a value into the vault under name (value is never returned).
func (c *Client) SetSecret(ctx context.Context, name, value string) (model.Secret, error) {
	var out model.Secret
	return out, c.do(ctx, http.MethodPost, "/v1/secrets", map[string]string{"name": name, "value": value}, &out)
}

// DeleteSecret removes a secret by name.
func (c *Client) DeleteSecret(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/secrets/"+name, nil, nil)
}

// ExportProject returns an encrypted bundle of the project (passphrase sent out-of-band).
func (c *Client) ExportProject(ctx context.Context, projectID, passphrase string, full bool) ([]byte, error) {
	url := c.baseURL + "/v1/projects/" + projectID + "/export"
	if full {
		url += "?full=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-OSB-Passphrase", passphrase)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("export: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// ImportBundle imports an encrypted bundle and returns the new project id.
func (c *Client) ImportBundle(ctx context.Context, data []byte, passphrase string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/import", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("X-OSB-Passphrase", passphrase)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("import: %s: %s", resp.Status, string(body))
	}
	var out struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ProjectID, nil
}

// ExtensionInfo is a loaded extension package's metadata.
type ExtensionInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Publisher     string   `json:"publisher"`
	Trusted       bool     `json:"trusted"`
	Digest        string   `json:"digest"`
	Capabilities  []string `json:"capabilities"`
	Methodologies []string `json:"methodologies"`
}

// HubPackage is a package listed in a hub index.
type HubPackage struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Publisher    string   `json:"publisher"`
	Description  string   `json:"description"`
	Tags         []string `json:"tags"`
	PublisherKey string   `json:"publisher_key"`
}

// HubIndex browses a hub's package index (via the control plane).
func (c *Client) HubIndex(ctx context.Context, hubURL string) ([]HubPackage, error) {
	var out struct {
		Packages []HubPackage `json:"packages"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/hub/index?url="+url.QueryEscape(hubURL), nil, &out)
	return out.Packages, err
}

// HubInstall installs a package from a hub. trust=true trusts the entry's publisher key first.
func (c *Client) HubInstall(ctx context.Context, hubURL, id string, trust, allowUnsigned bool) (ExtensionInfo, error) {
	var out ExtensionInfo
	body := map[string]any{"url": hubURL, "id": id, "trust": trust, "allow_unsigned": allowUnsigned}
	return out, c.do(ctx, http.MethodPost, "/v1/hub/install", body, &out)
}

// ListExtensions returns the loaded extension packages.
func (c *Client) ListExtensions(ctx context.Context) ([]ExtensionInfo, error) {
	var out []ExtensionInfo
	return out, c.do(ctx, http.MethodGet, "/v1/extensions", nil, &out)
}

// ListIntegrations returns available integration connector names.
func (c *Client) ListIntegrations(ctx context.Context) ([]string, error) {
	var out []string
	return out, c.do(ctx, http.MethodGet, "/v1/integrations", nil, &out)
}

// PushFindingRequest configures a finding push to an external tracker.
type PushFindingRequest struct {
	Integration string `json:"integration"`
	BaseURL     string `json:"base_url"`
	ProjectKey  string `json:"project_key,omitempty"`
	Credential  string `json:"credential,omitempty"` // vault secret NAME
}

// PushFinding sends a finding to an external tracker and returns the (idempotent) external link.
func (c *Client) PushFinding(ctx context.Context, findingID string, req PushFindingRequest) (model.ExternalLink, error) {
	var out model.ExternalLink
	return out, c.do(ctx, http.MethodPost, "/v1/findings/"+findingID+"/push", req, &out)
}

// ListFindingLinks returns a finding's external links.
func (c *Client) ListFindingLinks(ctx context.Context, findingID string) ([]model.ExternalLink, error) {
	var out []model.ExternalLink
	return out, c.do(ctx, http.MethodGet, "/v1/findings/"+findingID+"/links", nil, &out)
}

// ListCanaries returns planted canary tokens.
func (c *Client) ListCanaries(ctx context.Context) ([]model.Canary, error) {
	var out []model.Canary
	return out, c.do(ctx, http.MethodGet, "/v1/canaries", nil, &out)
}

// CreateCanary plants a new canary token with a label (the token is returned).
func (c *Client) CreateCanary(ctx context.Context, label string) (model.Canary, error) {
	var out model.Canary
	return out, c.do(ctx, http.MethodPost, "/v1/canaries", map[string]string{"label": label}, &out)
}

// DeleteCanary removes a canary by id.
func (c *Client) DeleteCanary(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/canaries/"+id, nil, nil)
}

// ListDLPEvents returns recent DLP events.
func (c *Client) ListDLPEvents(ctx context.Context, limit int) ([]model.DLPEvent, error) {
	path := "/v1/dlp-events"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out []model.DLPEvent
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// DerivedEgress is the derived-artifact egress policy (mode + tier) and the classification scale
// (ADR-0064/0065). Mode is "derived" or "inherit".
type DerivedEgress struct {
	Mode   string                      `json:"mode"`
	Tier   string                      `json:"tier"`
	Levels []model.ClassificationLevel `json:"levels"`
}

// GetDerivedEgress returns the derived-artifact egress policy. projectID "" reads the global default;
// otherwise the project's policy (via X-Project-Id), with the tier resolved through project → global.
func (c *Client) GetDerivedEgress(ctx context.Context, projectID string) (DerivedEgress, error) {
	var out DerivedEgress
	if projectID == "" {
		return out, c.do(ctx, http.MethodGet, "/v1/analyst/derived-egress", nil, &out)
	}
	return out, c.doHeaders(ctx, http.MethodGet, "/v1/analyst/derived-egress", map[string]string{projectHeader: projectID}, nil, &out)
}

// SetDerivedEgress sets the derived-artifact egress policy for a scope (projectID "" = global). Empty
// mode or tier leaves that field unchanged.
func (c *Client) SetDerivedEgress(ctx context.Context, projectID, mode, tier string) error {
	body := map[string]string{"mode": mode, "tier": tier}
	if projectID == "" {
		return c.do(ctx, http.MethodPut, "/v1/analyst/derived-egress", body, nil)
	}
	return c.doHeaders(ctx, http.MethodPut, "/v1/analyst/derived-egress", map[string]string{projectHeader: projectID}, body, nil)
}

// ListProjectKB returns the KB a project inherits from its targets.
func (c *Client) ListProjectKB(ctx context.Context, projectID string) ([]model.KBEntry, error) {
	var out []model.KBEntry
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/kb", nil, &out)
}

// ListTargetKB returns a target's KB entries.
func (c *Client) ListTargetKB(ctx context.Context, targetID string) ([]model.KBEntry, error) {
	var out []model.KBEntry
	return out, c.do(ctx, http.MethodGet, "/v1/targets/"+targetID+"/kb", nil, &out)
}

// NewKBEntry is a human-authored KB entry to create.
type NewKBEntry struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Body        string `json:"body,omitempty"`
	Tags        string `json:"tags,omitempty"`
	Sensitivity string `json:"sensitivity,omitempty"`
}

// CreateKBEntry adds a KB entry to a target.
func (c *Client) CreateKBEntry(ctx context.Context, targetID string, e NewKBEntry) (model.KBEntry, error) {
	var out model.KBEntry
	return out, c.do(ctx, http.MethodPost, "/v1/targets/"+targetID+"/kb", e, &out)
}

// ReviewKBEntry sets a KB entry's review state (confirmed | rejected | unreviewed).
func (c *Client) ReviewKBEntry(ctx context.Context, id, state string) (model.KBEntry, error) {
	var out model.KBEntry
	return out, c.do(ctx, http.MethodPost, "/v1/kb/"+id+"/review", map[string]string{"state": state}, &out)
}

// VerifyKBEntry bumps a KB entry's freshness — marks the fact as still true as of now (ADR-0043).
func (c *Client) VerifyKBEntry(ctx context.Context, id string) (model.KBEntry, error) {
	var out model.KBEntry
	return out, c.do(ctx, http.MethodPost, "/v1/kb/"+id+"/verify", nil, &out)
}

// ListMethodologies returns the methodology catalog (raw JSON packs).
func (c *Client) ListMethodologies(ctx context.Context) ([]methodologyPack, error) {
	var out []methodologyPack
	return out, c.do(ctx, http.MethodGet, "/v1/methodologies", nil, &out)
}

// methodologyPack is a light view of a catalog pack for the CLI.
type methodologyPack struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Tech  string `json:"tech"`
	Items []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"items"`
}

// MethodologyCoverage is a project's coverage view (opaque summary + packs).
type MethodologyCoverage struct {
	Packs []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Items []struct {
			Item struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"item"`
			Status string `json:"status"`
			Note   string `json:"note"`
		} `json:"items"`
	} `json:"packs"`
	Summary struct {
		Total      int `json:"total"`
		Covered    int `json:"covered"`
		CoveredPct int `json:"covered_pct"`
	} `json:"summary"`
}

// GetMethodologyCoverage returns a project's methodology coverage view.
func (c *Client) GetMethodologyCoverage(ctx context.Context, projectID string) (MethodologyCoverage, error) {
	var out MethodologyCoverage
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/methodology", nil, &out)
}

// StartPlan runs a playbook as a background plan for a project (ADR-0019).
func (c *Client) StartPlan(ctx context.Context, projectID, playbookID string) (model.Plan, error) {
	var out model.Plan
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/plans", map[string]string{"playbook_id": playbookID}, &out)
}

// GetPlan returns a plan with its steps (poll to watch a run's progress).
func (c *Client) GetPlan(ctx context.Context, id string) (model.Plan, error) {
	var out model.Plan
	return out, c.do(ctx, http.MethodGet, "/v1/plans/"+id, nil, &out)
}

// ListPlans returns a project's plans (without steps), newest first.
func (c *Client) ListPlans(ctx context.Context, projectID string) ([]model.Plan, error) {
	var out []model.Plan
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/plans", nil, &out)
}

// ResolvePlanGate approves or denies a plan's waiting approval gate and resumes the run (ADR-0044).
func (c *Client) ResolvePlanGate(ctx context.Context, planID, stepID string, approve bool, note string) (model.Plan, error) {
	var out model.Plan
	body := map[string]any{"approve": approve, "note": note}
	return out, c.do(ctx, http.MethodPost, "/v1/plans/"+planID+"/steps/"+stepID+"/resolve", body, &out)
}

// MethodologySuggestion recommends adopting a pack based on the KB.
type MethodologySuggestion struct {
	MethodologyID string `json:"methodology_id"`
	Title         string `json:"title"`
	Reason        string `json:"reason"`
}

// MethodologySuggestions returns KB-driven pack suggestions for a project.
func (c *Client) MethodologySuggestions(ctx context.Context, projectID string) ([]MethodologySuggestion, error) {
	var out []MethodologySuggestion
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/methodology/suggestions", nil, &out)
}

// AdoptMethodology adopts a methodology pack for a project.
func (c *Client) AdoptMethodology(ctx context.Context, projectID, methodologyID string) error {
	return c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/methodology/adopt",
		map[string]string{"methodology_id": methodologyID}, nil)
}

// SetCoverage records a project's status + note for a methodology item.
func (c *Client) SetCoverage(ctx context.Context, projectID, itemID, status, note string) error {
	return c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/coverage",
		map[string]string{"item_id": itemID, "status": status, "note": note}, nil)
}

// ReportTemplate is an available report template.
type ReportTemplate struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

// ListReportTemplates returns the available report templates.
func (c *Client) ListReportTemplates(ctx context.Context) ([]ReportTemplate, error) {
	var out []ReportTemplate
	return out, c.do(ctx, http.MethodGet, "/v1/report-templates", nil, &out)
}

// ListReports returns a project's generated reports, newest first.
func (c *Client) ListReports(ctx context.Context, projectID string) ([]model.Report, error) {
	var out []model.Report
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/reports", nil, &out)
}

// GenerateReport renders a report for a project (template id + format md|html).
func (c *Client) GenerateReport(ctx context.Context, projectID, template, format string) (model.Report, error) {
	var out model.Report
	body := map[string]string{"template": template, "format": format}
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/reports", body, &out)
}

// AuditVerification is the result of a hash-chain integrity check.
type AuditVerification struct {
	OK       bool   `json:"ok"`
	Events   int    `json:"events"`
	BrokenAt uint64 `json:"broken_at_seq"`
}

// VerifyAudit recomputes the audit hash chain and reports whether it is intact.
func (c *Client) VerifyAudit(ctx context.Context) (AuditVerification, error) {
	var out AuditVerification
	return out, c.do(ctx, http.MethodGet, "/v1/audit/verify", nil, &out)
}

// ListAudit returns recent audit events (newest first).
func (c *Client) ListAudit(ctx context.Context, limit int) ([]model.AuditEvent, error) {
	path := "/v1/audit"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out []model.AuditEvent
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ListSessions returns a project's interactive terminal sessions, newest first.
func (c *Client) ListSessions(ctx context.Context, projectID string) ([]model.Session, error) {
	var out []model.Session
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/sessions", nil, &out)
}

// OpenSession starts a sandboxed terminal session (attach interactively via the WebSocket).
func (c *Client) OpenSession(ctx context.Context, projectID, actor string) (model.Session, error) {
	var out model.Session
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/sessions", map[string]string{"actor": actor}, &out)
}

// GetSession returns a session by id.
func (c *Client) GetSession(ctx context.Context, id string) (model.Session, error) {
	var out model.Session
	return out, c.do(ctx, http.MethodGet, "/v1/sessions/"+id, nil, &out)
}

// CloseSession finalizes a session, capturing its transcript.
func (c *Client) CloseSession(ctx context.Context, id string) (model.Session, error) {
	var out model.Session
	return out, c.do(ctx, http.MethodPost, "/v1/sessions/"+id+"/close", nil, &out)
}

// SaveSessionEvidence promotes a closed session's transcript into an observation.
func (c *Client) SaveSessionEvidence(ctx context.Context, id, note string) (model.Observation, error) {
	var out model.Observation
	return out, c.do(ctx, http.MethodPost, "/v1/sessions/"+id+"/evidence", map[string]string{"note": note}, &out)
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

// ProjectSearch runs the omni-search scoped to a project (applications, assets, findings, observations) —
// the GUI's "search everywhere in this project" — by routing with X-Project-Id. A local read, no LLM.
func (c *Client) ProjectSearch(ctx context.Context, projectID, q string) ([]model.SearchResult, error) {
	var out []model.SearchResult
	return out, c.doHeaders(ctx, http.MethodGet, "/v1/search?q="+url.QueryEscape(q), map[string]string{projectHeader: projectID}, nil, &out)
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

// ArchiveThread soft-archives a thread (retained for audit, hidden from the active list).
func (c *Client) ArchiveThread(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/threads/"+id+"/archive", nil, nil)
}

// DeleteThread permanently purges a thread and its messages/approvals.
func (c *Client) DeleteThread(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/threads/"+id, nil, nil)
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

// DecideThreadApproval decides a gated tool's approval within a project thread (approve|deny), carrying
// X-Project-Id so the resumed turn routes to that project. Used by the TUI to approve from the terminal.
func (c *Client) DecideThreadApproval(ctx context.Context, projectID, id, decision string) (SendResult, error) {
	var out SendResult
	body := map[string]string{"decision": decision}
	return out, c.doHeaders(ctx, http.MethodPost, "/v1/approvals/"+id+"/decide", map[string]string{projectHeader: projectID}, body, &out)
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

// DeleteAsset removes an asset from its project.
func (c *Client) DeleteAsset(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/assets/"+id, nil, nil)
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
	ProjectID    *string        `json:"project_id,omitempty"`
	Actor        string         `json:"actor,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	RunnerID     string         `json:"runner_id,omitempty"` // "" = local; else an enrolled remote runner (ADR-0024)
}

// TaskOutcome is a completed task with its artifacts and interpreted observations.
type TaskOutcome struct {
	Task         model.Task          `json:"task"`
	Artifacts    []model.Artifact    `json:"artifacts"`
	Observations []model.Observation `json:"observations"`
}

// RunTask enqueues a capability run and returns the pending task (ADR-0022). The run executes
// asynchronously; poll GetTask until the status is terminal.
func (c *Client) RunTask(ctx context.Context, req RunTaskRequest) (model.Task, error) {
	var out model.Task
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

// RunnerView is an enrolled remote runner with live online status (ADR-0024).
type RunnerView struct {
	model.Runner
	Online bool `json:"online"`
}

// ListRunners returns the enrolled remote runners.
func (c *Client) ListRunners(ctx context.Context) ([]RunnerView, error) {
	var out []RunnerView
	return out, c.do(ctx, http.MethodGet, "/v1/runners", nil, &out)
}

// EnrollToken is a one-time runner enrollment token (returned once, at mint).
type EnrollToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// MintRunnerEnrollToken issues a one-time enrollment token for a new runner.
func (c *Client) MintRunnerEnrollToken(ctx context.Context, label string, ttlMinutes int) (EnrollToken, error) {
	var out EnrollToken
	return out, c.do(ctx, http.MethodPost, "/v1/runners/enroll-token",
		map[string]any{"label": label, "ttl_minutes": ttlMinutes}, &out)
}

// DeleteRunner revokes an enrolled remote runner.
func (c *Client) DeleteRunner(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/runners/"+id, nil, nil)
}

// ProjectIntegrations is the global connectors merged with this project's binding state (ADR-0027 / IA
// declutter).
type ProjectIntegrations struct {
	Connectors []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Type       string `json:"type"`
		BaseURL    string `json:"base_url"`
		Pullable   bool   `json:"pullable"`
		Bound      bool   `json:"bound"`
		ProjectKey string `json:"project_key"`
	} `json:"connectors"`
}

// PullResult is the outcome of an inbound integration pull.
type PullResult struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
	Total    int `json:"total"`
}

// ListConnectors returns the global external-tracker connectors.
func (c *Client) ListConnectors(ctx context.Context) ([]model.Connector, error) {
	var out []model.Connector
	return out, c.do(ctx, http.MethodGet, "/v1/connectors", nil, &out)
}

// CreateConnector registers a global connector (credential is a vault secret name).
func (c *Client) CreateConnector(ctx context.Context, name, typ, baseURL, credential string) (model.Connector, error) {
	var out model.Connector
	body := map[string]string{"name": name, "type": typ, "base_url": baseURL, "credential": credential}
	return out, c.do(ctx, http.MethodPost, "/v1/connectors", body, &out)
}

// DeleteConnector removes a global connector.
func (c *Client) DeleteConnector(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/connectors/"+id, nil, nil)
}

// ListProjectIntegrations returns the connectors merged with this project's binding state.
func (c *Client) ListProjectIntegrations(ctx context.Context, projectID string) (ProjectIntegrations, error) {
	var out ProjectIntegrations
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/integrations", nil, &out)
}

// SetBinding attaches a project to a global connector with a project-side scope (ADR-0027).
func (c *Client) SetBinding(ctx context.Context, projectID, connectorID, projectKey string) (model.IntegrationBinding, error) {
	var out model.IntegrationBinding
	body := map[string]string{"project_key": projectKey}
	return out, c.do(ctx, http.MethodPut, "/v1/projects/"+projectID+"/integrations/"+connectorID, body, &out)
}

// PullIntegration imports external findings from a connector into the project as observations.
func (c *Client) PullIntegration(ctx context.Context, projectID, connectorID string) (PullResult, error) {
	var out PullResult
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/integrations/"+connectorID+"/pull", nil, &out)
}

// ListInvestigations returns a project's disposition-opened investigations (ADR-0028).
func (c *Client) ListInvestigations(ctx context.Context, projectID string) ([]model.Investigation, error) {
	var out []model.Investigation
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/investigations", nil, &out)
}

// ReindexCorpus rebuilds the project's semantic index (ADR-0039), returning the chunk count.
func (c *Client) ReindexCorpus(ctx context.Context, projectID string) (int, error) {
	var out struct {
		Chunks int `json:"chunks"`
	}
	return out.Chunks, c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/reindex", nil, &out)
}

// SearchCorpus does semantic retrieval over the project's corpus + KB (ADR-0039).
func (c *Client) SearchCorpus(ctx context.Context, projectID, query string, k int) ([]store.ScoredChunk, error) {
	path := "/v1/projects/" + projectID + "/search-corpus?q=" + url.QueryEscape(query)
	if k > 0 {
		path += "&k=" + strconv.Itoa(k)
	}
	var out []store.ScoredChunk
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// Dossier returns the rendered "what we know" markdown brief for a target or project (ADR-0042). `kind` is
// "targets" or "projects".
func (c *Client) Dossier(ctx context.Context, kind, id string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/"+kind+"/"+id+"/dossier?format=markdown", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("dossier: %s", resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// ListProjectObservations returns a project's observations (ADR-0037); unreviewedOnly narrows to untriaged.
func (c *Client) ListProjectObservations(ctx context.Context, projectID string, unreviewedOnly bool) ([]model.Observation, error) {
	path := "/v1/projects/" + projectID + "/observations"
	if unreviewedOnly {
		path += "?unreviewed_only=true"
	}
	var out []model.Observation
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// RunInvestigation starts a vuln-validator agent thread for an investigation.
func (c *Client) RunInvestigation(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/investigations/"+id+"/run", nil, nil)
}

// SetInvestigationStatus resolves/dismisses/reopens an investigation.
func (c *Client) SetInvestigationStatus(ctx context.Context, id, status string) error {
	return c.do(ctx, http.MethodPost, "/v1/investigations/"+id+"/status", map[string]string{"status": status}, nil)
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

// ProjectObservations returns a project's observations (raw scanner output). This is a local API read —
// it never routes content to an LLM — so the TUI's /observations works regardless of egress clearance.
func (c *Client) ProjectObservations(ctx context.Context, projectID string) ([]model.Observation, error) {
	var out []model.Observation
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+projectID+"/observations", nil, &out)
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
	return c.doHeaders(ctx, method, path, nil, body, out)
}

// doHeaders is do with extra request headers — used to carry X-Project-Id so thread/analyst calls route
// to the right project's database (ADR-0049), the project's threads instead of the reserved global one.
func (c *Client) doHeaders(ctx context.Context, method, path string, headers map[string]string, body, out any) error {
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
	for k, v := range headers {
		req.Header.Set(k, v)
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
