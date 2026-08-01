package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/action"
)

// Custom actions (ADR-0059) are stored in the global.db, like saved profiles/playbooks, so an action is
// reusable across projects. Structured fields are persisted as JSON. The built-in example actions are
// code-defined (action.BuiltIns) and merged in at the service layer — only user-authored actions land here.

// CreateAction stores a user-authored action.
func (db *DB) CreateAction(ctx context.Context, a action.Action) (action.Action, error) {
	if err := validateAction(a); err != nil {
		return action.Action{}, err
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	ts := nowString()
	a.CreatedAt = parseTime(ts)
	a.UpdatedAt = a.CreatedAt
	if _, err := db.ExecContext(ctx,
		`INSERT INTO actions (id, name, description, icon, kind, subject_kinds, applies_when, technique,
			profile_id, instruction, image, cmd, network, timeout_seconds, memory_mb, cpus, output,
			created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Description, a.Icon, string(a.Kind), jsonStr(a.SubjectKinds), jsonStr(a.AppliesWhen),
		a.Technique, a.ProfileID, a.Instruction, a.Image, jsonStr(a.Cmd), a.Network, a.TimeoutSeconds,
		a.MemoryMB, a.CPUs, jsonStr(a.Output), ts, ts); err != nil {
		return action.Action{}, err
	}
	return a, nil
}

// UpdateAction replaces a user-authored action's mutable fields.
func (db *DB) UpdateAction(ctx context.Context, a action.Action) (action.Action, error) {
	if a.ID == "" {
		return action.Action{}, errors.New("store: update action needs an id")
	}
	if err := validateAction(a); err != nil {
		return action.Action{}, err
	}
	ts := nowString()
	res, err := db.ExecContext(ctx,
		`UPDATE actions SET name=?, description=?, icon=?, kind=?, subject_kinds=?, applies_when=?, technique=?,
			profile_id=?, instruction=?, image=?, cmd=?, network=?, timeout_seconds=?, memory_mb=?, cpus=?,
			output=?, updated_at=? WHERE id=?`,
		a.Name, a.Description, a.Icon, string(a.Kind), jsonStr(a.SubjectKinds), jsonStr(a.AppliesWhen),
		a.Technique, a.ProfileID, a.Instruction, a.Image, jsonStr(a.Cmd), a.Network, a.TimeoutSeconds,
		a.MemoryMB, a.CPUs, jsonStr(a.Output), ts, a.ID)
	if err != nil {
		return action.Action{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return action.Action{}, ErrNotFound
	}
	a.UpdatedAt = parseTime(ts)
	return a, nil
}

// GetAction returns one user-authored action by id.
func (db *DB) GetAction(ctx context.Context, id string) (action.Action, error) {
	row := db.QueryRowContext(ctx, actionSelect+` WHERE id = ?`, id)
	a, err := scanAction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return action.Action{}, ErrNotFound
	}
	return a, err
}

// ListActions returns all user-authored actions, newest first.
func (db *DB) ListActions(ctx context.Context) ([]action.Action, error) {
	rows, err := db.QueryContext(ctx, actionSelect+` ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []action.Action
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAction removes a user-authored action.
func (db *DB) DeleteAction(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM actions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const actionSelect = `SELECT id, name, description, icon, kind, subject_kinds, applies_when, technique,
	profile_id, instruction, image, cmd, network, timeout_seconds, memory_mb, cpus, output, created_at, updated_at
	FROM actions`

// scanner is the shared read interface of *sql.Row and *sql.Rows.
type scanner interface{ Scan(...any) error }

func scanAction(row scanner) (action.Action, error) {
	var a action.Action
	var kind, subjectKinds, appliesWhen, cmd, output, created, updated string
	if err := row.Scan(&a.ID, &a.Name, &a.Description, &a.Icon, &kind, &subjectKinds, &appliesWhen,
		&a.Technique, &a.ProfileID, &a.Instruction, &a.Image, &cmd, &a.Network, &a.TimeoutSeconds,
		&a.MemoryMB, &a.CPUs, &output, &created, &updated); err != nil {
		return action.Action{}, err
	}
	a.Kind = action.Kind(kind)
	_ = json.Unmarshal([]byte(subjectKinds), &a.SubjectKinds)
	_ = json.Unmarshal([]byte(appliesWhen), &a.AppliesWhen)
	_ = json.Unmarshal([]byte(cmd), &a.Cmd)
	_ = json.Unmarshal([]byte(output), &a.Output)
	a.CreatedAt = parseTime(created)
	a.UpdatedAt = parseTime(updated)
	return a, nil
}

func validateAction(a action.Action) error {
	if a.Name == "" {
		return errors.New("store: action needs a name")
	}
	if len(a.SubjectKinds) == 0 {
		return errors.New("store: action needs at least one subject kind")
	}
	switch a.Kind {
	case action.KindAgent:
		if a.ProfileID == "" || a.Instruction == "" {
			return errors.New("store: an agent action needs a profile and an instruction")
		}
	case action.KindScript:
		if a.Image == "" || len(a.Cmd) == 0 {
			return errors.New("store: a script action needs an image and a command")
		}
	default:
		return errors.New("store: action kind must be 'agent' or 'script'")
	}
	return nil
}

func jsonStr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
