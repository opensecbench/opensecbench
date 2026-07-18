package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// GetExternalLink returns the link for a finding + integration, or ErrNotFound.
func (db *DB) GetExternalLink(ctx context.Context, findingID, integration string) (model.ExternalLink, error) {
	var l model.ExternalLink
	var created string
	err := db.QueryRowContext(ctx,
		`SELECT id, finding_id, integration, external_id, external_url, created_at
		 FROM external_links WHERE finding_id = ? AND integration = ?`, findingID, integration).
		Scan(&l.ID, &l.FindingID, &l.Integration, &l.ExternalID, &l.ExternalURL, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ExternalLink{}, ErrNotFound
	}
	if err != nil {
		return model.ExternalLink{}, err
	}
	l.CreatedAt = parseTime(created)
	return l, nil
}

// CreateExternalLink records a finding↔external link.
func (db *DB) CreateExternalLink(ctx context.Context, l model.ExternalLink) (model.ExternalLink, error) {
	if l.FindingID == "" || l.Integration == "" || l.ExternalID == "" {
		return model.ExternalLink{}, errors.New("store: external link requires finding, integration, external id")
	}
	l.ID = uuid.NewString()
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO external_links (id, finding_id, integration, external_id, external_url, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		l.ID, l.FindingID, l.Integration, l.ExternalID, l.ExternalURL, ts); err != nil {
		return model.ExternalLink{}, err
	}
	l.CreatedAt = parseTime(ts)
	return l, nil
}

// ListExternalLinks returns all external links for a finding.
func (db *DB) ListExternalLinks(ctx context.Context, findingID string) ([]model.ExternalLink, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, finding_id, integration, external_id, external_url, created_at
		 FROM external_links WHERE finding_id = ? ORDER BY created_at`, findingID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.ExternalLink
	for rows.Next() {
		var l model.ExternalLink
		var created string
		if err := rows.Scan(&l.ID, &l.FindingID, &l.Integration, &l.ExternalID, &l.ExternalURL, &created); err != nil {
			return nil, err
		}
		l.CreatedAt = parseTime(created)
		out = append(out, l)
	}
	return out, rows.Err()
}
