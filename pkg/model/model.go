// Package model holds the core domain types (ADR-0002). It has no dependencies on other
// control-plane packages so it can be shared freely by the store, API, and clients.
package model

import "time"

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
