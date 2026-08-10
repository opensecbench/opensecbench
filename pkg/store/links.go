package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateLink inserts an entity link.
func (db *DB) CreateLink(ctx context.Context, l model.EntityLink) (model.EntityLink, error) {
	if l.SourceType == "" || l.SourceID == "" || l.TargetType == "" || l.TargetID == "" || l.Relationship == "" {
		return model.EntityLink{}, errors.New("store: link requires source, target, and relationship")
	}
	l.ID = uuid.NewString()
	metaJSON, _ := json.Marshal(l.Metadata)
	if l.Metadata == nil {
		metaJSON = []byte("{}")
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO entity_links (id, source_type, source_id, relationship, target_type, target_id, metadata, note, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.SourceType, l.SourceID, l.Relationship, l.TargetType, l.TargetID,
		string(metaJSON), l.Note, ts); err != nil {
		return model.EntityLink{}, err
	}
	l.CreatedAt = parseTime(ts)
	return l, nil
}

// ListLinks returns all links originating from or targeting the given entity.
func (db *DB) ListLinks(ctx context.Context, entityType, entityID string) ([]model.EntityLink, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, source_type, source_id, relationship, target_type, target_id, metadata, note, created_at
		 FROM entity_links
		 WHERE (source_type = ? AND source_id = ?) OR (target_type = ? AND target_id = ?)
		 ORDER BY created_at`,
		entityType, entityID, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanLinks(rows)
}

// ListLinksByRelationship returns all links of a given relationship type.
func (db *DB) ListLinksByRelationship(ctx context.Context, relationship string) ([]model.EntityLink, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, source_type, source_id, relationship, target_type, target_id, metadata, note, created_at
		 FROM entity_links WHERE relationship = ? ORDER BY created_at`, relationship)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanLinks(rows)
}

// DeleteLink removes an entity link.
func (db *DB) DeleteLink(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM entity_links WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanLinks(rows *sql.Rows) ([]model.EntityLink, error) {
	var out []model.EntityLink
	for rows.Next() {
		var l model.EntityLink
		var meta, created string
		var note sql.NullString
		if err := rows.Scan(&l.ID, &l.SourceType, &l.SourceID, &l.Relationship,
			&l.TargetType, &l.TargetID, &meta, &note, &created); err != nil {
			return nil, err
		}
		l.Metadata = parseJSONStringMap(meta)
		if note.Valid {
			l.Note = note.String
		}
		l.CreatedAt = parseTime(created)
		out = append(out, l)
	}
	return out, rows.Err()
}
