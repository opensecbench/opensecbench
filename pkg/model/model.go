// Package model holds the core domain types (ADR-0002). It has no dependencies on other
// control-plane packages so it can be shared freely by the store, API, and clients.
package model

import (
	"encoding/json"
	"strings"
	"time"
)

// Organization is an optional top-level grouping ("if used").
type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Target is a durable real-world system that survives across engagements and anchors the
// knowledge base and prior coverage.
type Target struct {
	ID             string    `json:"id"`
	OrganizationID *string   `json:"organization_id,omitempty"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Project is a time-boxed engagement referencing one or more targets.
type Project struct {
	ID             string    `json:"id"`
	OrganizationID *string   `json:"organization_id,omitempty"`
	GroupID        *string   `json:"group_id,omitempty"`
	Name           string    `json:"name"`
	Status         string    `json:"status"`
	TargetIDs      []string  `json:"target_ids"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Scope dispositions (ADR-0051): a scope entry is either an in-scope allow rule or an out-of-scope exclusion.
const (
	ScopeAllow = "allow"
	ScopeDeny  = "deny"
)

// ScopeEntry is one scope rule for a project — an in-scope allow rule or an out-of-scope (deny) exclusion.
type ScopeEntry struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Kind        string    `json:"kind"`        // host | domain | cidr
	Value       string    `json:"value"`
	Disposition string    `json:"disposition"` // allow | deny (default allow)
	CreatedAt   time.Time `json:"created_at"`
}

// Engagement environments, data-sensitivity classes, and contact roles (ADR-0051).
const (
	EnvProduction = "production"
	EnvStaging    = "staging"
	EnvDev        = "dev"
	EnvMixed      = "mixed"

	DataOpen       = "open"
	DataPrivate    = "private"
	DataRestricted = "restricted"

	ContactTechnical  = "technical"
	ContactAuthorizer = "authorizer"
	ContactBreakGlass = "breakglass"
)

// Engagement is the frame of an assessment (ADR-0051): identity, scope posture, rules of engagement, timeline,
// contacts, and reporting captured at setup. One per project; every field is optional, so a project with no
// engagement row behaves exactly as before. Kinds is the assessment type(s) (web, api, code, cloud, …).
// Techniques is a map of allow-flags (intrusive, automated_exploit, brute_force, dos, social, destructive)
// that gates which capabilities may run. DataClass tightens external-provider egress for this engagement.
type Engagement struct {
	ProjectID string `json:"project_id"`
	// BasePath is the project's root directory on disk (ADR-0051). Relative asset locations resolve against
	// it, so an operator points at the codebase once and adds assets by relative path. Empty = unset.
	BasePath      string                  `json:"base_path,omitempty"`
	Kinds         []string                `json:"kinds,omitempty"`
	Objective     string                  `json:"objective,omitempty"`
	Reference     string                  `json:"reference,omitempty"`
	Environment   string                  `json:"environment,omitempty"`
	DataClass     string                  `json:"data_class,omitempty"`
	Standard      string                  `json:"standard,omitempty"`
	Compliance    string                  `json:"compliance,omitempty"`
	SeverityScale string                  `json:"severity_scale,omitempty"`
	Authorized    bool                    `json:"authorized"`
	Authorizer    string                  `json:"authorizer,omitempty"`
	AuthRef       string                  `json:"auth_ref,omitempty"`
	AuthFrom      string                  `json:"auth_from,omitempty"` // ISO date (YYYY-MM-DD)
	AuthTo        string                  `json:"auth_to,omitempty"`
	WindowStart   string                  `json:"window_start,omitempty"`
	WindowEnd     string                  `json:"window_end,omitempty"`
	ReportDue     string                  `json:"report_due,omitempty"`
	Techniques    map[string]bool         `json:"techniques,omitempty"`
	Notes         string                  `json:"notes,omitempty"`
	Contacts      []EngagementContact     `json:"contacts,omitempty"`
	TestAccounts  []EngagementTestAccount `json:"test_accounts,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

// EngagementContact is a point of contact for an engagement (technical POC, authorizer, or break-glass).
type EngagementContact struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Role      string `json:"role"`
	Name      string `json:"name"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
	Note      string `json:"note,omitempty"`
}

// EngagementTestAccount is a test credential for the engagement. The password is never stored here — only a
// SecretRef into the vault (ADR-0011); Username/Role are for authorization/IDOR testing across roles.
type EngagementTestAccount struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Role      string `json:"role"`
	Username  string `json:"username,omitempty"`
	SecretRef string `json:"secret_ref,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Proxy match/replace rule targets (ADR-0016 Step 4).
const (
	RuleTargetURL            = "url"
	RuleTargetRequestHeader  = "request_header"
	RuleTargetRequestBody    = "request_body"
	RuleTargetResponseHeader = "response_header"
	RuleTargetResponseBody   = "response_body"
)

// UsageRecord is one Analyst run's token usage, tagged for per-project, per-model/vendor comparison and
// per-agent attribution. Provider/Model record the backend that actually ran the request (which, under
// cross-provider tag routing, may differ from the active provider); AgentType names the profile.
type UsageRecord struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id,omitempty"`
	ThreadID     string    `json:"thread_id,omitempty"`
	AgentType    string    `json:"agent_type,omitempty"`
	Provider     string    `json:"provider"` // vendor/type
	Model        string    `json:"model"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CreatedAt    time.Time `json:"created_at"`
}

// UsageByModel aggregates token usage for one (provider, model) pair.
type UsageByModel struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Runs         int    `json:"runs"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// UsageByAgent aggregates token usage for one agent profile (agent_type).
type UsageByAgent struct {
	AgentType    string `json:"agent_type"`
	Runs         int    `json:"runs"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// UsageSummary is a workbench-wide token-spend roll-up for the Home cockpit: this-month and all-time
// totals plus the heaviest (provider, model) pairs and agents. Informational — there is no budget cap.
type UsageSummary struct {
	MonthInput  int            `json:"month_input"`
	MonthOutput int            `json:"month_output"`
	AllInput    int            `json:"all_input"`
	AllOutput   int            `json:"all_output"`
	TopModels   []UsageByModel `json:"top_models"`
	TopAgents   []UsageByAgent `json:"top_agents"`
}

// Provider is a registered LLM backend for the Analyst (ADR-0006). KeySealed is the vault-sealed
// credential and is never serialized to clients.
type Provider struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Model     string    `json:"model"` // optional default model (ADR-0052 back-compat); the connection serves many
	BaseURL   string    `json:"base_url"`
	KeySealed string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	// ModelsRefreshedAt is when this connection's model set was last discovered (ADR-0052); zero = never.
	ModelsRefreshedAt time.Time `json:"models_refreshed_at,omitempty"`
}

// ConnectionModel is one model a connection can serve, discovered from the backend and enriched by the
// curated overlay (ADR-0052). Cached in connection_models so the picker and routing reflect what the
// backend actually serves. Source is "live" | "overlay" | "custom".
type ConnectionModel struct {
	ConnectionID  string    `json:"connection_id"`
	ModelID       string    `json:"model_id"`
	DisplayName   string    `json:"display_name"`
	Family        string    `json:"family"`
	ContextWindow int       `json:"context_window"`
	InputPerMTok  float64   `json:"input_per_mtok"`
	OutputPerMTok float64   `json:"output_per_mtok"`
	Tags          []string  `json:"tags"`
	Source        string    `json:"source"`
	LastSeen      time.Time `json:"last_seen"`
}

// Runner statuses.
const (
	RunnerActive  = "active"
	RunnerRevoked = "revoked"
)

// Runner is an enrolled remote runner (ADR-0024): an outbound-connect agent that executes capability
// tasks from its own network vantage, authenticated by the ed25519 PubKey established at enrollment.
type Runner struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	PubKey     string     `json:"pubkey"` // base64 ed25519 public key
	Status     string     `json:"status"`
	EnrolledAt time.Time  `json:"enrolled_at"`
	LastSeen   *time.Time `json:"last_seen,omitempty"`
}

// ProxyRule is a per-project match/replace rule applied by the proxy's traffic-processor pipeline.
type ProxyRule struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Enabled   bool      `json:"enabled"`
	Target    string    `json:"target"`
	Match     string    `json:"match"` // regular expression
	Replace   string    `json:"replace"`
	CreatedAt time.Time `json:"created_at"`
}

// IntegrationConfig is a reusable per-project connection to an external tracker (ADR-0027). Credential is
// a vault secret NAME, never a value.
type IntegrationConfig struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Integration string    `json:"integration"`
	BaseURL     string    `json:"base_url"`
	ProjectKey  string    `json:"project_key"`
	Credential  string    `json:"credential"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ExternalLink ties an OSB finding to an issue in an external tracker (idempotent per integration).
type ExternalLink struct {
	ID          string    `json:"id"`
	FindingID   string    `json:"finding_id"`
	Integration string    `json:"integration"`
	ExternalID  string    `json:"external_id"`
	ExternalURL string    `json:"external_url"`
	CreatedAt   time.Time `json:"created_at"`
}

// Canary is a planted decoy token (exfil tripwire) — if it appears at an egress, DLP alerts.
type Canary struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

// DLPEvent is a recorded DLP hit at an egress point (ADR-0011).
type DLPEvent struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`   // secret | canary | pattern
	Label     string    `json:"label"`  // secret/canary name or pattern name
	Action    string    `json:"action"` // block | alert
	Blocked   bool      `json:"blocked"`
	Location  string    `json:"location,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Secret is vault metadata (ADR-0011). The sealed value is never exposed through this type.
type Secret struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// KB entry kinds and scopes (ADR-0010).
const (
	KBArchitecture = "architecture"
	KBAuth         = "auth"
	KBEndpoint     = "endpoint"
	KBTechStack    = "tech_stack"
	KBEnvironment  = "environment"
	KBDataFlow     = "data_flow"
	KBConvention   = "convention"
	KBGotcha       = "gotcha"
	KBTactic       = "tactic"

	KBScopeTarget = "target"
	KBScopeGroup  = "group"
	KBScopeOrg    = "org"
	KBScopeGlobal = "global"
)

// KBEntry is durable knowledge about a target that survives across engagements (ADR-0010). Agent-
// drafted entries (origin=thread) start unreviewed and are curated by a human, like observations.
type KBEntry struct {
	ID string `json:"id"`
	// Anchor: exactly one of TargetID/GroupID/OrganizationID is set per Scope (all empty = global). A
	// project inherits its target(s) + group + org + global knowledge (ADR-0041).
	TargetID       string `json:"target_id,omitempty"`
	GroupID        string `json:"group_id,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	Kind           string `json:"kind"`
	Scope          string `json:"scope"`
	Title          string `json:"title"`
	Body           string `json:"body,omitempty"`
	Tags           string `json:"tags,omitempty"`
	Sensitivity    string `json:"sensitivity"`
	Origin         string `json:"origin"`
	ReviewState    string `json:"review_state"`
	SourceRef      string `json:"source_ref,omitempty"`
	// LastVerifiedAt is when the fact was last affirmatively checked to still hold (ADR-0043). Confirming a
	// draft stamps it; the agent bumps it (verify_kb_entry) when it re-observes a known fact. Zero = never
	// verified (an unreviewed draft). Old-but-verified entries go stale so accumulated knowledge doesn't rot.
	LastVerifiedAt time.Time `json:"last_verified_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Methodology coverage statuses (ADR-0009).
const (
	CoverageNotStarted    = "not_started"
	CoverageInProgress    = "in_progress"
	CoverageCovered       = "covered"
	CoverageNotApplicable = "not_applicable"
)

// CoverageEntry is a project's recorded status for one methodology item.
type CoverageEntry struct {
	ProjectID string    `json:"project_id"`
	ItemID    string    `json:"item_id"`
	Status    string    `json:"status"`
	Note      string    `json:"note,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Notification kinds.
const (
	NotifyApproval = "approval"
	NotifyReport   = "report"
	NotifyTask     = "task"
	NotifyInfo     = "info"
)

// Notification is an in-app, needs-attention event surfaced to the operator (P8). A `link` is an
// optional client hint like "approval:<id>" or "report:<id>".
type Notification struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	ProjectID *string   `json:"project_id,omitempty"`
	Link      string    `json:"link,omitempty"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

// Report is a generated engagement deliverable (ADR-0008). Its rendered bytes are a CAS artifact.
type Report struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	TemplateID string    `json:"template_id"`
	Format     string    `json:"format"`
	Title      string    `json:"title"`
	ArtifactID string    `json:"artifact_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// AuditEvent is one immutable, hash-chained entry in the append-only audit trail (ADR-0002).
type AuditEvent struct {
	Seq      uint64          `json:"seq"`
	Time     time.Time       `json:"time"`
	Actor    string          `json:"actor"`
	Action   string          `json:"action"`
	Target   string          `json:"target,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	PrevHash string          `json:"prev_hash"`
	Hash     string          `json:"hash"`
}

// Session kinds and statuses.
const (
	SessionTerminal = "terminal"

	SessionActive = "active"
	SessionClosed = "closed"
	SessionError  = "error"
)

// Session is an interactive terminal opened through a runner (ADR-0007). Its full transcript is
// captured to the CAS on close and referenced here, so it is auditable and capturable as evidence.
type Session struct {
	ID                   string     `json:"id"`
	ProjectID            string     `json:"project_id"`
	Kind                 string     `json:"kind"`
	Runner               string     `json:"runner"`
	Container            string     `json:"container"`
	Image                string     `json:"image"`
	Status               string     `json:"status"`
	Actor                string     `json:"actor"`
	TranscriptArtifactID *string    `json:"transcript_artifact_id,omitempty"`
	Error                string     `json:"error,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	ClosedAt             *time.Time `json:"closed_at,omitempty"`
}

// HTTP exchange origins.
const (
	ExchangeReplay = "replay"
	ExchangeProxy  = "proxy"
)

// HTTPExchange is a request and (once sent) its response, anchored to a project (ADR-0007). The
// Replay edits and resends it; save-as-evidence promotes a response into the CAS.
type HTTPExchange struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"project_id"`
	Name            string     `json:"name"`
	Origin          string     `json:"origin"`
	Method          string     `json:"method"`
	URL             string     `json:"url"`
	RequestHeaders  string     `json:"request_headers"`
	RequestBody     string     `json:"request_body"`
	Status          *int       `json:"status,omitempty"`
	ResponseHeaders string     `json:"response_headers"`
	ResponseBody    string     `json:"response_body"`
	DurationMS      *int       `json:"duration_ms,omitempty"`
	Egress          string     `json:"egress,omitempty"` // "" = control-plane host; else the runner id (ADR-0025)
	// TLS is a JSON summary of the upstream server certificate captured for a proxied HTTPS exchange
	// (subject/issuer/validity + flags: expired, hostname mismatch, self-signed, untrusted). Empty for
	// plain HTTP or when no cert was presented.
	TLS       string     `json:"tls,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
}

// Playbook run statuses.
const (
	PlaybookRunning   = "running"
	PlaybookSucceeded = "succeeded"
	PlaybookFailed    = "failed"
)

// PlaybookRun groups the tasks produced by running a playbook against an asset.
type PlaybookRun struct {
	ID         string     `json:"id"`
	PlaybookID string     `json:"playbook_id"`
	AssetID    *string    `json:"asset_id,omitempty"`
	Actor      string     `json:"actor"`
	Status     string     `json:"status"`
	TaskIDs    []string   `json:"task_ids"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Thread statuses.
const (
	ThreadActive           = "active"
	ThreadAwaitingApproval = "awaiting_approval"
	ThreadDone             = "done"
	ThreadError            = "error"
)

// Thread is a persisted Analyst conversation (forkable).
type Thread struct {
	ID             string    `json:"id"`
	ProjectID      *string   `json:"project_id,omitempty"`
	ParentThreadID *string   `json:"parent_thread_id,omitempty"`
	ForkSeq        *int      `json:"fork_seq,omitempty"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	Provider       string    `json:"provider"`
	AgentType      string    `json:"agent_type"` // the driving agent profile (ADR-0019); default "generalist"
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Message is one turn in a thread. ToolCalls (on an assistant turn) and ToolCallID/ToolError (on a
// "tool" turn) carry the canonical tool interaction (ADR-0017), persisted so a thread is vendor-portable.
type Message struct {
	ID         string          `json:"id"`
	ThreadID   string          `json:"thread_id"`
	Seq        int             `json:"seq"`
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolError  bool            `json:"tool_error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Plan / plan-step statuses (ADR-0019). PlanWaiting/StepWaiting are the mid-run approval pause (ADR-0044):
// a gate step, once its dependencies complete, holds the plan until a human approves it.
const (
	PlanRunning = "running"
	PlanWaiting = "waiting"
	PlanDone    = "done"
	PlanFailed  = "failed"

	StepPending = "pending"
	StepRunning = "running"
	StepWaiting = "waiting"
	StepDone    = "done"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// SavedProfile is a user-defined agent profile (ADR-0019 step 4): a persona + a tool allow-list (a JSON
// array of tool names).
type SavedProfile struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Persona     string          `json:"persona"`
	Tools       json.RawMessage `json:"tools"`
	ModelTag    string          `json:"model_tag"` // routing tag (ADR-0052); empty = the default list
	CreatedAt   time.Time       `json:"created_at"`
}

// Schedule runs a playbook on a cadence for a project (ADR-0019 step 4).
type Schedule struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"project_id"`
	PlaybookID      string     `json:"playbook_id"`
	IntervalSeconds int        `json:"interval_seconds"`
	Enabled         bool       `json:"enabled"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	NextRunAt       time.Time  `json:"next_run_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

// SavedPlaybook is a user-saved agent playbook (ADR-0019): recorded from a run or authored directly.
// Steps is a JSON array of {key, profile, instruction, depends_on}.
type SavedPlaybook struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Goal        string          `json:"goal"`
	Steps       json.RawMessage `json:"steps"`
	Source      string          `json:"source,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Plan is a running agent playbook — a DAG of steps executed in dependency order (ADR-0019).
type Plan struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	PlaybookID string     `json:"playbook_id"`
	Goal       string     `json:"goal"`
	Status     string     `json:"status"`
	Steps      []PlanStep `json:"steps,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// PlanStep is one step of a plan: a sub-task delegated to a profile, with its dependencies and outcome.
// A Gate step pauses the plan for human approval once its dependencies complete, before it runs (ADR-0044);
// GateApproved records that a human cleared it, so a resumed run proceeds instead of pausing again.
type PlanStep struct {
	ID           string   `json:"id"`
	PlanID       string   `json:"plan_id"`
	Seq          int      `json:"seq"`
	Key          string   `json:"key"`
	Profile      string   `json:"profile"`
	Instruction  string   `json:"instruction"`
	DependsOn    []string `json:"depends_on"`
	Gate         bool     `json:"gate,omitempty"`
	GateApproved bool     `json:"gate_approved,omitempty"`
	Status       string   `json:"status"`
	Result       string   `json:"result,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// Approval statuses.
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalDenied   = "denied"
)

// Approval is a gated tool call awaiting a human decision.
type Approval struct {
	ID        string          `json:"id"`
	ThreadID  string          `json:"thread_id"`
	Tool      string          `json:"tool"`
	Args      json.RawMessage `json:"args"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	DecidedAt *time.Time      `json:"decided_at,omitempty"`
}

// Context item types.
const (
	ContextDocument = "document"
	ContextEmail    = "email"
	ContextChat     = "chat"
	ContextNote     = "note"
)

// ContextItem is ingested unstructured context (a doc, email, chat log, or note) whose bytes
// live in the CAS as an input artifact, linked to a project.
type ContextItem struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	ApplicationID *string   `json:"application_id,omitempty"`
	Type          string    `json:"type"`
	Name          string    `json:"name"`
	ArtifactID    string    `json:"artifact_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// SearchResult is one hit from omni-search, across entity kinds.
type SearchResult struct {
	Kind   string `json:"kind"` // project | application | asset | finding | observation
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// Application is a service/app under a project (engagement).
type Application struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Asset sensitivity and type enums (mirrored by CHECK constraints in the schema).
const (
	SensitivityOpenSource = "open_source"
	SensitivityPrivate    = "private"

	AssetSourceRepo      = "source_repo"
	AssetCloudDeployment = "cloud_deployment"
	AssetInfrastructure  = "infrastructure"
	AssetDocument        = "document"
	AssetCorrespondence  = "correspondence"
)

// InferSensitivity guesses an asset's sensitivity from its location, defaulting to private (the
// safe default for a security tool). Callers may override the result.
//
// TODO(P1+): smarter inference (e.g. resolve a git remote against known public hosts).
func InferSensitivity(location string) string {
	l := strings.ToLower(location)
	for _, hint := range []string{"/oss/", "/opensource/", "/open-source/", "/public/", "/third_party/", "/third-party/", "/vendor/"} {
		if strings.Contains(l, hint) {
			return SensitivityOpenSource
		}
	}
	return SensitivityPrivate
}

// Asset is a scoped item under an application (a repo, cloud deployment, doc, ...).
type Asset struct {
	ID            string    `json:"id"`
	ApplicationID string    `json:"application_id"`
	Type          string    `json:"type"`
	Location      string    `json:"location"`
	Sensitivity   string    `json:"sensitivity"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Task status values (mirrored by a CHECK constraint in the schema).
const (
	TaskPending   = "pending" // queued, not yet picked up by a worker (async execution, ADR-0022)
	TaskRunning   = "running"
	TaskSucceeded = "succeeded"
	TaskFailed    = "failed"
)

// Task is one capability invocation and the root of a provenance chain (ADR-0004).
type Task struct {
	ID                string          `json:"id"`
	CapabilityID      string          `json:"capability_id"`
	CapabilityVersion string          `json:"capability_version"`
	ApplicationID     *string         `json:"application_id,omitempty"`
	AssetID           *string         `json:"asset_id,omitempty"`
	ProjectID         *string         `json:"project_id,omitempty"`
	Actor             string          `json:"actor"`
	Runner            string          `json:"runner"`
	Params            json.RawMessage `json:"params,omitempty"`
	Status            string          `json:"status"`
	ExitCode          *int            `json:"exit_code,omitempty"`
	Error             string          `json:"error,omitempty"`
	Attempts          int             `json:"attempts,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	StartedAt         *time.Time      `json:"started_at,omitempty"`
	FinishedAt        *time.Time      `json:"finished_at,omitempty"`

	// Reconstruction data for the durable queue (ADR-0023) — needed to re-run a queued task after a
	// restart. Never serialized to clients: SecretRefs holds vault-secret NAMES (never values), and
	// TargetDir is a local filesystem path.
	SecretRefs map[string]string `json:"-"`
	TargetDir  string            `json:"-"`

	// RunnerTarget selects where the task runs (ADR-0024): '' = the local Docker runner; otherwise a
	// runners.id. Persisted so the durable queue re-dispatches to the right runner after a restart.
	RunnerTarget string `json:"runner_target,omitempty"`
}

// Artifact kinds.
const (
	ArtifactInput  = "input"
	ArtifactOutput = "output"
)

// Artifact is an immutable output stored in the CAS and linked to the task that produced it.
type Artifact struct {
	ID        string    `json:"id"`
	TaskID    *string   `json:"task_id,omitempty"`
	SHA256    string    `json:"sha256"`
	MediaType string    `json:"media_type"`
	Size      int64     `json:"size"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Observation origins and review states (ADR-0005).
const (
	OriginTool   = "tool"
	OriginThread = "thread"
	OriginHuman  = "human"

	ReviewUnreviewed = "unreviewed"
	ReviewConfirmed  = "confirmed"
	ReviewRejected   = "rejected"
)

// Observation is an interpreted result awaiting or having undergone human review. It scopes to a project
// either via its task or, when it has no task (integration pull, analyst thread), a direct ProjectID.
type Observation struct {
	ID          string  `json:"id"`
	TaskID      *string `json:"task_id,omitempty"`
	ArtifactID  *string `json:"artifact_id,omitempty"`
	ProjectID   *string `json:"project_id,omitempty"`
	Origin      string  `json:"origin"`
	ReviewState string  `json:"review_state"`
	Title       string  `json:"title"`
	Detail      string  `json:"detail,omitempty"`
	Severity    string  `json:"severity"`
	RuleID      string  `json:"rule_id,omitempty"`
	Location    string  `json:"location,omitempty"`
	// Attributes are structured facts an interpreter attaches (e.g. TruffleHog verified=true) that
	// post-run disposition rules match on (ADR-0028).
	Attributes map[string]string `json:"attributes,omitempty"`
	// Fingerprint is a stable content hash (origin|rule|location|detail) used to dedup the same finding
	// across re-scans, so it is not re-created or re-dispositioned (ADR-0029).
	Fingerprint string    `json:"fingerprint,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Route is a declared HTTP entry point of an application (ADR-0033) — extracted from source (route-map) or
// discovered from captured traffic. Observed=true means captured proxy traffic matched it, confirming it is
// exposed; a finding whose location file equals HandlerFile is tied to this route.
type Route struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Method      string    `json:"method,omitempty"` // GET/POST/... or "" = any/unknown
	Path        string    `json:"path"`
	HandlerFile string    `json:"handler_file,omitempty"` // "" for a traffic-only route (no source)
	HandlerLine int       `json:"handler_line,omitempty"`
	Framework   string    `json:"framework,omitempty"`
	Source      string    `json:"source,omitempty"` // capability that produced it, or "traffic"
	Observed    bool      `json:"observed"`         // matched captured traffic → confirmed exposed
	UpdatedAt   time.Time `json:"updated_at"`
}

// Investigation statuses.
const (
	InvestigationOpen          = "open"
	InvestigationInvestigating = "investigating"
	InvestigationResolved      = "resolved"
	InvestigationDismissed     = "dismissed"
)

// Investigation is a tracked follow-up opened by a disposition rule for an observation that needs
// validation (ADR-0028) — worked by a human and/or a seeded agent thread, ending in a validated finding.
type Investigation struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	ApplicationID *string   `json:"application_id,omitempty"`
	ObservationID string    `json:"observation_id"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	ThreadID      *string   `json:"thread_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// DispositionRule is a per-project override of a capability's manifest-declared routing (ADR-0028).
type DispositionRule struct {
	ID           string            `json:"id"`
	ProjectID    string            `json:"project_id"`
	CapabilityID string            `json:"capability_id"` // "" = all capabilities
	When         map[string]string `json:"when"`
	MinSeverity  string            `json:"min_severity,omitempty"`
	Action       string            `json:"action"`
	Priority     int               `json:"priority"`
	CreatedAt    time.Time         `json:"created_at"`
}

// Finding statuses.
const (
	FindingOpen          = "open"
	FindingConfirmed     = "confirmed"
	FindingRemediated    = "remediated"
	FindingAccepted      = "accepted"
	FindingFalsePositive = "false_positive"
)

// Finding is a reviewed security conclusion, supported by confirmed observations.
type Finding struct {
	ID             string    `json:"id"`
	ApplicationID  *string   `json:"application_id,omitempty"`
	Title          string    `json:"title"`
	Severity       string    `json:"severity"`
	Status         string    `json:"status"`
	Description    string    `json:"description,omitempty"`
	CWE            string    `json:"cwe,omitempty"`
	ObservationIDs []string  `json:"observation_ids"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
