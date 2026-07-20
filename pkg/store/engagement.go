package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// Engagement record storage (ADR-0051). The engagement is 1:1 with a project; contacts and test accounts are
// child rows. SetEngagement upserts the whole record atomically (row + children), which matches the setup
// modal's save-the-form semantics. GetEngagement returns (zero, ErrNotFound) when a project has no record, so
// callers can treat "no engagement configured" distinctly from an all-defaults one.

// GetEngagement loads a project's engagement record, including contacts and test accounts.
func (db *DB) GetEngagement(ctx context.Context, projectID string) (model.Engagement, error) {
	var e model.Engagement
	var kinds, techniques, created, updated string
	var authorized int
	err := db.QueryRowContext(ctx,
		`SELECT project_id, kinds, objective, reference, environment, data_class, standard, compliance,
		        severity_scale, authorized, authorizer, auth_ref, auth_from, auth_to,
		        window_start, window_end, report_due, techniques, notes, created_at, updated_at
		 FROM engagement WHERE project_id = ?`, projectID).
		Scan(&e.ProjectID, &kinds, &e.Objective, &e.Reference, &e.Environment, &e.DataClass, &e.Standard,
			&e.Compliance, &e.SeverityScale, &authorized, &e.Authorizer, &e.AuthRef, &e.AuthFrom, &e.AuthTo,
			&e.WindowStart, &e.WindowEnd, &e.ReportDue, &techniques, &e.Notes, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Engagement{}, ErrNotFound
	}
	if err != nil {
		return model.Engagement{}, err
	}
	e.Kinds = splitCSV(kinds)
	e.Authorized = authorized != 0
	if techniques != "" {
		_ = json.Unmarshal([]byte(techniques), &e.Techniques)
	}
	e.CreatedAt = parseTime(created)
	e.UpdatedAt = parseTime(updated)
	if e.Contacts, err = db.listContacts(ctx, projectID); err != nil {
		return model.Engagement{}, err
	}
	if e.TestAccounts, err = db.listTestAccounts(ctx, projectID); err != nil {
		return model.Engagement{}, err
	}
	return e, nil
}

// SetEngagement upserts a project's engagement record and replaces its contacts and test accounts, all in one
// transaction. It preserves created_at on update.
func (db *DB) SetEngagement(ctx context.Context, e model.Engagement) (model.Engagement, error) {
	if e.ProjectID == "" {
		return model.Engagement{}, errors.New("store: engagement requires project_id")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.Engagement{}, err
	}
	defer func() { _ = tx.Rollback() }()

	now := nowString()
	created := now
	var existing string
	if err := tx.QueryRowContext(ctx, `SELECT created_at FROM engagement WHERE project_id = ?`, e.ProjectID).
		Scan(&existing); err == nil {
		created = existing
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.Engagement{}, err
	}

	techniques := ""
	if len(e.Techniques) > 0 {
		b, _ := json.Marshal(e.Techniques)
		techniques = string(b)
	}
	authorized := 0
	if e.Authorized {
		authorized = 1
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO engagement
		 (project_id, kinds, objective, reference, environment, data_class, standard, compliance,
		  severity_scale, authorized, authorizer, auth_ref, auth_from, auth_to,
		  window_start, window_end, report_due, techniques, notes, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(project_id) DO UPDATE SET
		  kinds=excluded.kinds, objective=excluded.objective, reference=excluded.reference,
		  environment=excluded.environment, data_class=excluded.data_class, standard=excluded.standard,
		  compliance=excluded.compliance, severity_scale=excluded.severity_scale, authorized=excluded.authorized,
		  authorizer=excluded.authorizer, auth_ref=excluded.auth_ref, auth_from=excluded.auth_from,
		  auth_to=excluded.auth_to, window_start=excluded.window_start, window_end=excluded.window_end,
		  report_due=excluded.report_due, techniques=excluded.techniques, notes=excluded.notes,
		  updated_at=excluded.updated_at`,
		e.ProjectID, strings.Join(e.Kinds, ","), e.Objective, e.Reference, e.Environment, e.DataClass,
		e.Standard, e.Compliance, e.SeverityScale, authorized, e.Authorizer, e.AuthRef, e.AuthFrom, e.AuthTo,
		e.WindowStart, e.WindowEnd, e.ReportDue, techniques, e.Notes, created, now); err != nil {
		return model.Engagement{}, err
	}

	// Replace children.
	if _, err := tx.ExecContext(ctx, `DELETE FROM engagement_contacts WHERE project_id = ?`, e.ProjectID); err != nil {
		return model.Engagement{}, err
	}
	for _, c := range e.Contacts {
		if strings.TrimSpace(c.Name) == "" && strings.TrimSpace(c.Email) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO engagement_contacts (id, project_id, role, name, email, phone, note) VALUES (?,?,?,?,?,?,?)`,
			uuid.NewString(), e.ProjectID, c.Role, c.Name, c.Email, c.Phone, c.Note); err != nil {
			return model.Engagement{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engagement_test_accounts WHERE project_id = ?`, e.ProjectID); err != nil {
		return model.Engagement{}, err
	}
	for _, a := range e.TestAccounts {
		if strings.TrimSpace(a.Username) == "" && strings.TrimSpace(a.Role) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO engagement_test_accounts (id, project_id, role, username, secret_ref, note) VALUES (?,?,?,?,?,?)`,
			uuid.NewString(), e.ProjectID, a.Role, a.Username, a.SecretRef, a.Note); err != nil {
			return model.Engagement{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Engagement{}, err
	}
	return db.GetEngagement(ctx, e.ProjectID)
}

func (db *DB) listContacts(ctx context.Context, projectID string) ([]model.EngagementContact, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, role, name, email, phone, note FROM engagement_contacts WHERE project_id = ? ORDER BY role`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.EngagementContact{}
	for rows.Next() {
		var c model.EngagementContact
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Role, &c.Name, &c.Email, &c.Phone, &c.Note); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (db *DB) listTestAccounts(ctx context.Context, projectID string) ([]model.EngagementTestAccount, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, role, username, secret_ref, note FROM engagement_test_accounts WHERE project_id = ? ORDER BY role`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.EngagementTestAccount{}
	for rows.Next() {
		var a model.EngagementTestAccount
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Role, &a.Username, &a.SecretRef, &a.Note); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
