package store

import (
	"context"
	"encoding/json"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// ReplaceConnectionModels swaps a connection's cached model set (ADR-0052) for the freshly discovered +
// enriched one and stamps the connection's models_refreshed_at. Done in a single transaction so a partial
// refresh never leaves a mix of old and new rows.
func (db *DB) ReplaceConnectionModels(ctx context.Context, connID string, models []model.ConnectionModel) error {
	ts := nowString()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM connection_models WHERE connection_id = ?`, connID); err != nil {
		return err
	}
	for _, m := range models {
		tags, _ := json.Marshal(m.Tags)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO connection_models
			   (connection_id, model_id, display_name, family, context_window, input_per_mtok, output_per_mtok, tags, source, last_seen)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			connID, m.ModelID, m.DisplayName, m.Family, m.ContextWindow, m.InputPerMTok, m.OutputPerMTok, string(tags), m.Source, ts); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE providers SET models_refreshed_at = ? WHERE id = ?`, ts, connID); err != nil {
		return err
	}
	return tx.Commit()
}

// ListConnectionModels returns a connection's cached models, ordered by family then id for a stable UI.
func (db *DB) ListConnectionModels(ctx context.Context, connID string) ([]model.ConnectionModel, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT model_id, display_name, family, context_window, input_per_mtok, output_per_mtok, tags, source, last_seen
		   FROM connection_models WHERE connection_id = ? ORDER BY family, model_id`, connID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.ConnectionModel
	for rows.Next() {
		m := model.ConnectionModel{ConnectionID: connID}
		var tags, seen string
		if err := rows.Scan(&m.ModelID, &m.DisplayName, &m.Family, &m.ContextWindow,
			&m.InputPerMTok, &m.OutputPerMTok, &tags, &m.Source, &seen); err != nil {
			return nil, err
		}
		if tags != "" {
			_ = json.Unmarshal([]byte(tags), &m.Tags)
		}
		m.LastSeen = parseTime(seen)
		out = append(out, m)
	}
	return out, rows.Err()
}
