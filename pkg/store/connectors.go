package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateConnector registers a global external-tracker connection (ADR-0027 / IA declutter). Credential
// (if any) is a vault secret name, never a value.
func (db *DB) CreateConnector(ctx context.Context, c model.Connector) (model.Connector, error) {
	if c.Name == "" || c.Type == "" {
		return model.Connector{}, fmt.Errorf("store: connector needs a name and type")
	}
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO connectors (id, name, type, base_url, credential, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Type, c.BaseURL, c.Credential, ts); err != nil {
		return model.Connector{}, err
	}
	c.CreatedAt = parseTime(ts)
	return c, nil
}

const connectorCols = `id, name, type, base_url, credential, created_at`

func scanConnector(s interface{ Scan(...any) error }) (model.Connector, error) {
	var c model.Connector
	var created string
	if err := s.Scan(&c.ID, &c.Name, &c.Type, &c.BaseURL, &c.Credential, &created); err != nil {
		return model.Connector{}, err
	}
	c.CreatedAt = parseTime(created)
	return c, nil
}

// ListConnectors returns all registered connectors, oldest first.
func (db *DB) ListConnectors(ctx context.Context) ([]model.Connector, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+connectorCols+` FROM connectors ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Connector
	for rows.Next() {
		c, err := scanConnector(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConnector returns one connector by id.
func (db *DB) GetConnector(ctx context.Context, id string) (model.Connector, error) {
	c, err := scanConnector(db.QueryRowContext(ctx, `SELECT `+connectorCols+` FROM connectors WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Connector{}, ErrNotFound
	}
	return c, err
}

// DeleteConnector removes a connector.
func (db *DB) DeleteConnector(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM connectors WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
