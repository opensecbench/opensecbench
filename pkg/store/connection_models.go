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

	// A per-model clearance override is operator intent, not discovered metadata — carry it across a
	// refresh so re-discovery doesn't silently re-open a model the operator pinned to a lower tier.
	type override struct{ clearance, note string }
	kept := map[string]override{}
	rows, err := tx.QueryContext(ctx, `SELECT model_id, data_clearance, clearance_note FROM connection_models WHERE connection_id = ? AND data_clearance != ''`, connID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, cl, note string
		if err := rows.Scan(&id, &cl, &note); err != nil {
			_ = rows.Close()
			return err
		}
		kept[id] = override{cl, note}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	if _, err := tx.ExecContext(ctx, `DELETE FROM connection_models WHERE connection_id = ?`, connID); err != nil {
		return err
	}
	for _, m := range models {
		tags, _ := json.Marshal(m.Tags)
		clearance, note := m.DataClearance, m.ClearanceNote
		if ov, ok := kept[m.ModelID]; ok && clearance == "" {
			clearance, note = ov.clearance, ov.note // preserve a prior override the discovery didn't set
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO connection_models
			   (connection_id, model_id, display_name, family, context_window, input_per_mtok, output_per_mtok, tags, source, last_seen, data_clearance, clearance_note)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			connID, m.ModelID, m.DisplayName, m.Family, m.ContextWindow, m.InputPerMTok, m.OutputPerMTok, string(tags), m.Source, ts, clearance, note); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE providers SET models_refreshed_at = ? WHERE id = ?`, ts, connID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetConnectionModelClearance sets a per-model clearance override (and its note). An empty clearance
// clears the override, so the model inherits its connection's clearance again.
func (db *DB) SetConnectionModelClearance(ctx context.Context, connID, modelID, clearance, note string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE connection_models SET data_clearance = ?, clearance_note = ? WHERE connection_id = ? AND model_id = ?`,
		clearance, note, connID, modelID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ConnectionModelClearance returns a model's clearance override, or "" if the model has none (inherit the
// connection) or isn't cached. Used at the egress gate to resolve the effective clearance for a routed model.
func (db *DB) ConnectionModelClearance(ctx context.Context, connID, modelID string) string {
	var cl string
	_ = db.QueryRowContext(ctx, `SELECT data_clearance FROM connection_models WHERE connection_id = ? AND model_id = ?`, connID, modelID).Scan(&cl)
	return cl
}

// ListConnectionModels returns a connection's cached models, ordered by family then id for a stable UI.
func (db *DB) ListConnectionModels(ctx context.Context, connID string) ([]model.ConnectionModel, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT model_id, display_name, family, context_window, input_per_mtok, output_per_mtok, tags, source, last_seen, data_clearance, clearance_note
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
			&m.InputPerMTok, &m.OutputPerMTok, &tags, &m.Source, &seen, &m.DataClearance, &m.ClearanceNote); err != nil {
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
