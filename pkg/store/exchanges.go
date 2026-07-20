package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

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
		`INSERT INTO http_exchanges (id, project_id, name, origin, method, url, request_headers, request_body, tls, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.ProjectID, e.Name, e.Origin, e.Method, e.URL, e.RequestHeaders, e.RequestBody, e.TLS, ts); err != nil {
		return model.HTTPExchange{}, err
	}
	e.CreatedAt = parseTime(ts)
	return e, nil
}

// RecordResponse stores a send's response onto an exchange. egress records the vantage the request went
// out from ("" = control-plane host; otherwise the runner id, ADR-0025).
func (db *DB) RecordResponse(ctx context.Context, id string, status int, headers, body string, durationMS int, egress string) error {
	ts := nowString()
	res, err := db.ExecContext(ctx,
		`UPDATE http_exchanges
		 SET status = ?, response_headers = ?, response_body = ?, duration_ms = ?, sent_at = ?, egress = ?
		 WHERE id = ?`,
		status, headers, body, durationMS, ts, egress, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const exchangeCols = `id, project_id, name, origin, method, url, request_headers, request_body,
	status, response_headers, response_body, duration_ms, egress, tls, created_at, sent_at`

func scanExchange(s interface {
	Scan(dest ...any) error
}) (model.HTTPExchange, error) {
	var e model.HTTPExchange
	var status, duration sql.NullInt64
	var created string
	var sent sql.NullString
	if err := s.Scan(&e.ID, &e.ProjectID, &e.Name, &e.Origin, &e.Method, &e.URL,
		&e.RequestHeaders, &e.RequestBody, &status, &e.ResponseHeaders, &e.ResponseBody,
		&duration, &e.Egress, &e.TLS, &created, &sent); err != nil {
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
	return db.ListExchangesFiltered(ctx, projectID, ExchangeFilter{})
}

// ExchangeFilter narrows a project's exchange history. Zero-value fields are ignored.
type ExchangeFilter struct {
	Origin string // "" | "proxy" | "replay"
	Method string // exact HTTP method
	Status int    // 0 = any; exact status code otherwise
	Query  string // case-insensitive substring of the URL
	Limit  int    // 0 = default (200)
}

// ListExchangesFiltered returns a project's exchanges, newest first, narrowed by filter and capped.
func (db *DB) ListExchangesFiltered(ctx context.Context, projectID string, f ExchangeFilter) ([]model.HTTPExchange, error) {
	q := `SELECT ` + exchangeCols + ` FROM http_exchanges WHERE project_id = ?`
	args := []any{projectID}
	if f.Origin != "" {
		q += ` AND origin = ?`
		args = append(args, f.Origin)
	}
	if f.Method != "" {
		q += ` AND method = ?`
		args = append(args, f.Method)
	}
	if f.Status != 0 {
		q += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Query != "" {
		q += ` AND url LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(f.Query)+"%")
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, q, args...)
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

// escapeLike neutralizes LIKE wildcards in user input so a query is a literal substring match.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
