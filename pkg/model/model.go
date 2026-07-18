// Package model holds the core domain types (ADR-0002). It has no dependencies on other
// control-plane packages so it can be shared freely by the store, API, and clients.
package model

import (
	"encoding/json"
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
