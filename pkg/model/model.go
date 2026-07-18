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

// ScopeEntry is one in-scope allowlist rule for a project.
type ScopeEntry struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Kind      string    `json:"kind"` // host | domain | cidr
	Value     string    `json:"value"`
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
	ID          string    `json:"id"`
	TargetID    string    `json:"target_id"`
	Kind        string    `json:"kind"`
	Scope       string    `json:"scope"`
	Title       string    `json:"title"`
	Body        string    `json:"body,omitempty"`
	Tags        string    `json:"tags,omitempty"`
	Sensitivity string    `json:"sensitivity"`
	Origin      string    `json:"origin"`
	ReviewState string    `json:"review_state"`
	SourceRef   string    `json:"source_ref,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	ExchangeRepeater = "repeater"
	ExchangeProxy    = "proxy"
)

// HTTPExchange is a request and (once sent) its response, anchored to a project (ADR-0007). The
// Repeater edits and resends it; save-as-evidence promotes a response into the CAS.
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
	CreatedAt       time.Time  `json:"created_at"`
	SentAt          *time.Time `json:"sent_at,omitempty"`
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
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Message is one turn in a thread.
type Message struct {
	ID        string    `json:"id"`
	ThreadID  string    `json:"thread_id"`
	Seq       int       `json:"seq"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
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
	Actor             string          `json:"actor"`
	Runner            string          `json:"runner"`
	Params            json.RawMessage `json:"params,omitempty"`
	Status            string          `json:"status"`
	ExitCode          *int            `json:"exit_code,omitempty"`
	Error             string          `json:"error,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	StartedAt         *time.Time      `json:"started_at,omitempty"`
	FinishedAt        *time.Time      `json:"finished_at,omitempty"`
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

// Observation is an interpreted result awaiting or having undergone human review.
type Observation struct {
	ID          string    `json:"id"`
	TaskID      *string   `json:"task_id,omitempty"`
	ArtifactID  *string   `json:"artifact_id,omitempty"`
	Origin      string    `json:"origin"`
	ReviewState string    `json:"review_state"`
	Title       string    `json:"title"`
	Detail      string    `json:"detail,omitempty"`
	Severity    string    `json:"severity"`
	RuleID      string    `json:"rule_id,omitempty"`
	Location    string    `json:"location,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
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
