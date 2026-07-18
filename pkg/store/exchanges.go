package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateExchange records a new (unsent) HTTP exchange for the Replay.
func (db *DB) CreateExchange(ctx context.Context, e model.HTTPExchange) (model.HTTPExchange, error) {
	if e.ProjectID == "" || e.URL == "" {
		return model.HTTPExchange{}, errors.New("store: exchange project id and url required")
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.Method == "" {
		e.Method = "GET"
	}
	if e.Origin == "" {
		e.Origin = model.ExchangeReplay
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO http_exchanges (id, project_id, name, origin, method, url, request_headers, request_body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.ProjectID, e.Name, e.Origin, e.Method, e.URL, e.RequestHeaders, e.RequestBody, ts); err != nil {
		return model.HTTPExchange{}, err
	}
	e.CreatedAt = parseTime(ts)
	return e, nil
}

// RecordResponse stores a send's response onto an exchange.
func (db *DB) RecordResponse(ctx context.Context, id string, status int, headers, body string, durationMS int) error {
	ts := nowString()
	res, err := db.ExecContext(ctx,
		`UPDATE http_exchanges
		 SET status = ?, response_headers = ?, response_body = ?, duration_ms = ?, sent_at = ?
		 WHERE id = ?`,
		status, headers, body, durationMS, ts, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const exchangeCols = `id, project_id, name, origin, method, url, request_headers, request_body,
	status, response_headers, response_body, duration_ms, created_at, sent_at`

func scanExchange(s interface {
	Scan(dest ...any) error
}) (model.HTTPExchange, error) {
	var e model.HTTPExchange
	var status, duration sql.NullInt64
	var created string
	var sent sql.NullString
	if err := s.Scan(&e.ID, &e.ProjectID, &e.Name, &e.Origin, &e.Method, &e.URL,
		&e.RequestHeaders, &e.RequestBody, &status, &e.ResponseHeaders, &e.ResponseBody,
		&duration, &created, &sent); err != nil {
		return model.HTTPExchange{}, err
	}
	if status.Valid {
		v := int(status.Int64)
		e.Status = &v
	}
	if duration.Valid {
		v := int(duration.Int64)
		e.DurationMS = &v
	}
	e.CreatedAt = parseTime(created)
	e.SentAt = ptrTime(sent)
	return e, nil
}

// GetExchange returns an exchange by id.
func (db *DB) GetExchange(ctx context.Context, id string) (model.HTTPExchange, error) {
	row := db.QueryRowContext(ctx, `SELECT `+exchangeCols+` FROM http_exchanges WHERE id = ?`, id)
	e, err := scanExchange(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.HTTPExchange{}, ErrNotFound
	}
	return e, err
}

// ListExchangesByProject returns a project's exchanges, newest first.
func (db *DB) ListExchangesByProject(ctx context.Context, projectID string) ([]model.HTTPExchange, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+exchangeCols+` FROM http_exchanges WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.HTTPExchange
	for rows.Next() {
		e, err := scanExchange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
