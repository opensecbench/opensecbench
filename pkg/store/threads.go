package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// NewThread is the input for creating a thread.
type NewThread struct {
	ProjectID      *string
	Title          string
	Provider       string
	ParentThreadID *string
	ForkSeq        *int
}

// CreateThread inserts a thread.
func (db *DB) CreateThread(ctx context.Context, nt NewThread) (model.Thread, error) {
	t := model.Thread{
		ID:             uuid.NewString(),
		ProjectID:      nt.ProjectID,
		ParentThreadID: nt.ParentThreadID,
		ForkSeq:        nt.ForkSeq,
		Title:          nt.Title,
		Status:         model.ThreadActive,
		Provider:       nt.Provider,
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO threads (id, project_id, parent_thread_id, fork_seq, title, status, provider, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, nt.ProjectID, nt.ParentThreadID, nt.ForkSeq, t.Title, t.Status, t.Provider, ts, ts); err != nil {
		return model.Thread{}, err
	}
	t.CreatedAt, t.UpdatedAt = parseTime(ts), parseTime(ts)
	return t, nil
}

func scanThread(row interface{ Scan(...any) error }) (model.Thread, error) {
	var t model.Thread
	var project, parent sql.NullString
	var forkSeq sql.NullInt64
	var created, updated string
	if err := row.Scan(&t.ID, &project, &parent, &forkSeq, &t.Title, &t.Status, &t.Provider, &created, &updated); err != nil {
		return model.Thread{}, err
	}
	t.ProjectID, t.ParentThreadID = ptr(project), ptr(parent)
	if forkSeq.Valid {
		v := int(forkSeq.Int64)
		t.ForkSeq = &v
	}
	t.CreatedAt, t.UpdatedAt = parseTime(created), parseTime(updated)
	return t, nil
}

const threadCols = `id, project_id, parent_thread_id, fork_seq, title, status, provider, created_at, updated_at`

// GetThread returns a thread by id.
func (db *DB) GetThread(ctx context.Context, id string) (model.Thread, error) {
	t, err := scanThread(db.QueryRowContext(ctx, `SELECT `+threadCols+` FROM threads WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Thread{}, ErrNotFound
	}
	return t, err
}

// ListThreads returns all threads, newest first.
func (db *DB) ListThreads(ctx context.Context) ([]model.Thread, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+threadCols+` FROM threads ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Thread
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateThreadStatus sets a thread's status and touches updated_at.
func (db *DB) UpdateThreadStatus(ctx context.Context, id, status string) error {
	res, err := db.ExecContext(ctx, `UPDATE threads SET status = ?, updated_at = ? WHERE id = ?`, status, nowString(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AppendMessage adds a plain text message (system/user/assistant) to a thread.
func (db *DB) AppendMessage(ctx context.Context, threadID, role, content string) (model.Message, error) {
	return db.AppendMessageFull(ctx, model.Message{ThreadID: threadID, Role: role, Content: content})
}

// AppendMessageFull adds a message to a thread, assigning the next sequence number and persisting its
// canonical tool fields (ToolCalls/ToolCallID/ToolError, ADR-0017). ID/Seq/CreatedAt are assigned here.
func (db *DB) AppendMessageFull(ctx context.Context, in model.Message) (model.Message, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.Message{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var seq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), -1) + 1 FROM messages WHERE thread_id = ?`, in.ThreadID).Scan(&seq); err != nil {
		return model.Message{}, err
	}
	m := in
	m.ID = uuid.NewString()
	m.Seq = seq
	ts := nowString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages (id, thread_id, seq, role, content, tool_calls, tool_call_id, tool_error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ThreadID, seq, m.Role, m.Content, string(m.ToolCalls), m.ToolCallID, boolToInt(m.ToolError), ts); err != nil {
		return model.Message{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE threads SET updated_at = ? WHERE id = ?`, ts, m.ThreadID); err != nil {
		return model.Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Message{}, err
	}
	m.CreatedAt = parseTime(ts)
	return m, nil
}

// ListMessages returns a thread's messages in order.
func (db *DB) ListMessages(ctx context.Context, threadID string) ([]model.Message, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, thread_id, seq, role, content, tool_calls, tool_call_id, tool_error, created_at FROM messages WHERE thread_id = ? ORDER BY seq`, threadID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Message
	for rows.Next() {
		var m model.Message
		var created, toolCalls string
		var toolErr int
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.Seq, &m.Role, &m.Content, &toolCalls, &m.ToolCallID, &toolErr, &created); err != nil {
			return nil, err
		}
		if toolCalls != "" {
			m.ToolCalls = json.RawMessage(toolCalls)
		}
		m.ToolError = toolErr != 0
		m.CreatedAt = parseTime(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ForkThread creates a new thread branched from an existing one, copying messages up to and
// including atSeq.
func (db *DB) ForkThread(ctx context.Context, id string, atSeq int) (model.Thread, error) {
	parent, err := db.GetThread(ctx, id)
	if err != nil {
		return model.Thread{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.Thread{}, err
	}
	defer func() { _ = tx.Rollback() }()

	child := model.Thread{
		ID:             uuid.NewString(),
		ProjectID:      parent.ProjectID,
		ParentThreadID: &parent.ID,
		ForkSeq:        &atSeq,
		Title:          parent.Title + " (fork)",
		Status:         model.ThreadActive,
		Provider:       parent.Provider,
	}
	ts := nowString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO threads (id, project_id, parent_thread_id, fork_seq, title, status, provider, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		child.ID, child.ProjectID, child.ParentThreadID, child.ForkSeq, child.Title, child.Status, child.Provider, ts, ts); err != nil {
		return model.Thread{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages (id, thread_id, seq, role, content, tool_calls, tool_call_id, tool_error, created_at)
		 SELECT lower(hex(randomblob(16))), ?, seq, role, content, tool_calls, tool_call_id, tool_error, created_at FROM messages WHERE thread_id = ? AND seq <= ? ORDER BY seq`,
		child.ID, parent.ID, atSeq); err != nil {
		return model.Thread{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Thread{}, err
	}
	child.CreatedAt, child.UpdatedAt = parseTime(ts), parseTime(ts)
	return child, nil
}

// --- approvals ---

// CreateApproval records a pending gated tool call.
func (db *DB) CreateApproval(ctx context.Context, threadID, tool string, args json.RawMessage) (model.Approval, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	a := model.Approval{ID: uuid.NewString(), ThreadID: threadID, Tool: tool, Args: args, Status: model.ApprovalPending}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO approvals (id, thread_id, tool, args, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, threadID, tool, string(args), a.Status, ts); err != nil {
		return model.Approval{}, err
	}
	a.CreatedAt = parseTime(ts)
	return a, nil
}

// GetApproval returns an approval by id.
func (db *DB) GetApproval(ctx context.Context, id string) (model.Approval, error) {
	var a model.Approval
	var args string
	var created string
	var decided sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT id, thread_id, tool, args, status, created_at, decided_at FROM approvals WHERE id = ?`, id).
		Scan(&a.ID, &a.ThreadID, &a.Tool, &args, &a.Status, &created, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Approval{}, ErrNotFound
	}
	if err != nil {
		return model.Approval{}, err
	}
	a.Args = json.RawMessage(args)
	a.CreatedAt = parseTime(created)
	a.DecidedAt = ptrTime(decided)
	return a, nil
}

// ListPendingApprovals returns all pending approvals, oldest first.
func (db *DB) ListPendingApprovals(ctx context.Context) ([]model.Approval, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, thread_id, tool, args, status, created_at, decided_at FROM approvals WHERE status = 'pending' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Approval
	for rows.Next() {
		var a model.Approval
		var args, created string
		var decided sql.NullString
		if err := rows.Scan(&a.ID, &a.ThreadID, &a.Tool, &args, &a.Status, &created, &decided); err != nil {
			return nil, err
		}
		a.Args = json.RawMessage(args)
		a.CreatedAt = parseTime(created)
		a.DecidedAt = ptrTime(decided)
		out = append(out, a)
	}
	return out, rows.Err()
}

// DecideApproval sets a pending approval's status (approved | denied). It errors if the approval
// is not pending.
func (db *DB) DecideApproval(ctx context.Context, id, status string) (model.Approval, error) {
	if status != model.ApprovalApproved && status != model.ApprovalDenied {
		return model.Approval{}, fmt.Errorf("store: invalid approval status %q", status)
	}
	res, err := db.ExecContext(ctx,
		`UPDATE approvals SET status = ?, decided_at = ? WHERE id = ? AND status = 'pending'`,
		status, nowString(), id)
	if err != nil {
		return model.Approval{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Either not found or already decided.
		if _, gerr := db.GetApproval(ctx, id); errors.Is(gerr, ErrNotFound) {
			return model.Approval{}, ErrNotFound
		}
		return model.Approval{}, errors.New("store: approval already decided")
	}
	return db.GetApproval(ctx, id)
}
