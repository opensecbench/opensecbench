package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// This file holds the Manager-level operations that span databases in the two-tier layout (ADR-0049):
// project lifecycle (a project lives in its own database + the global index), the durable task queue
// (which is inherently cross-project), cross-project list views, and the cross-database KB-by-project
// query. Every method is mode-aware: in combined backing it delegates to the single database so behavior
// (and tests) are unchanged; in the split backing it fans out over per-project databases.

// activeProjectIDs returns the ids to fan out over — every project in the global index.
func (m *Manager) activeProjectIDs(ctx context.Context) ([]string, error) {
	rows, err := m.global.ListProjectIndex(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// --- Project lifecycle ---

// CreateProject creates a project: in split mode it provisions the project's own database, writes the
// project row there, and registers it in the global index; in combined mode it writes to the one database.
func (m *Manager) CreateProject(ctx context.Context, np NewProject) (model.Project, error) {
	if m.combined {
		p, err := m.global.CreateProject(ctx, np)
		if err != nil {
			return model.Project{}, err
		}
		return p, m.global.UpsertProjectIndex(ctx, p.ID, p.Name, p.Status)
	}
	id := uuid.NewString()
	pdb, err := m.Project(id)
	if err != nil {
		return model.Project{}, err
	}
	p, err := pdb.CreateProjectWithID(ctx, id, np)
	if err != nil {
		return model.Project{}, err
	}
	return p, m.global.UpsertProjectIndex(ctx, p.ID, p.Name, p.Status)
}

// GetProject reads a project from its own database (split) or the single database (combined). It checks
// the global index first in split mode so a missing id returns ErrNotFound without provisioning a dir.
func (m *Manager) GetProject(ctx context.Context, id string) (model.Project, error) {
	if m.combined {
		return m.global.GetProject(ctx, id)
	}
	known, err := m.projectIndexed(ctx, id)
	if err != nil {
		return model.Project{}, err
	}
	if !known {
		return model.Project{}, ErrNotFound
	}
	pdb, err := m.Project(id)
	if err != nil {
		return model.Project{}, err
	}
	return pdb.GetProject(ctx, id)
}

// ListProjects returns every project. Combined reads the one table; split reads the index and loads each
// project row from its database (project counts are small on a local workbench, and handles are cached).
func (m *Manager) ListProjects(ctx context.Context) ([]model.Project, error) {
	if m.combined {
		return m.global.ListProjects(ctx)
	}
	ids, err := m.activeProjectIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Project, 0, len(ids))
	for _, id := range ids {
		p, err := m.GetProject(ctx, id)
		if err != nil {
			continue // a torn-down project may linger a moment in the index; skip it
		}
		out = append(out, p)
	}
	return out, nil
}

// DeleteProject removes a project entirely. In split mode this is a directory purge (db + cas + workspace)
// plus the index row; in combined mode it deletes the row from the one database.
func (m *Manager) DeleteProject(ctx context.Context, id string) error {
	if m.combined {
		return m.global.DeleteProject(ctx, id)
	}
	if known, err := m.projectIndexed(ctx, id); err != nil {
		return err
	} else if !known {
		return ErrNotFound
	}
	return m.PurgeProject(ctx, id)
}

// projectIndexed reports whether a project id is present in the global index.
func (m *Manager) projectIndexed(ctx context.Context, id string) (bool, error) {
	var n int
	if err := m.global.QueryRowContext(ctx, `SELECT count(*) FROM project_index WHERE id = ?`, id).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// --- Durable task queue (cross-project) ---

// ClaimNextPendingTask atomically claims the next pending task. Combined claims from the one database;
// split scans each project's database and returns the first claim that succeeds (per-project atomicity is
// preserved; strict global oldest-first is relaxed to per-project ordering, fine for a local workbench).
func (m *Manager) ClaimNextPendingTask(ctx context.Context) (model.Task, bool, error) {
	if m.combined {
		return m.global.ClaimNextPendingTask(ctx)
	}
	ids, err := m.activeProjectIDs(ctx)
	if err != nil {
		return model.Task{}, false, err
	}
	for _, id := range ids {
		pdb, err := m.Project(id)
		if err != nil {
			continue
		}
		if t, ok, err := pdb.ClaimNextPendingTask(ctx); err != nil {
			return model.Task{}, false, err
		} else if ok {
			return t, true, nil
		}
	}
	return model.Task{}, false, nil
}

// RequeueInterruptedTasks resets tasks left "running" by a crash back to pending, across all projects.
func (m *Manager) RequeueInterruptedTasks(ctx context.Context) (int, error) {
	if m.combined {
		return m.global.RequeueInterruptedTasks(ctx)
	}
	ids, err := m.activeProjectIDs(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		pdb, err := m.Project(id)
		if err != nil {
			continue
		}
		if n, err := pdb.RequeueInterruptedTasks(ctx); err == nil {
			total += n
		}
	}
	return total, nil
}

// FailUnfinishedPlaybookRuns fails runs left "running" by a crash, across all projects.
func (m *Manager) FailUnfinishedPlaybookRuns(ctx context.Context) (int, error) {
	if m.combined {
		return m.global.FailUnfinishedPlaybookRuns(ctx)
	}
	ids, err := m.activeProjectIDs(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		pdb, err := m.Project(id)
		if err != nil {
			continue
		}
		if n, err := pdb.FailUnfinishedPlaybookRuns(ctx); err == nil {
			total += n
		}
	}
	return total, nil
}

// --- Cross-project list views (dashboards / inboxes) ---

// ListAllFindings returns findings across every project.
func (m *Manager) ListAllFindings(ctx context.Context) ([]model.Finding, error) {
	if m.combined {
		return m.global.ListFindings(ctx)
	}
	var out []model.Finding
	err := m.eachProject(ctx, func(pdb *DB) error {
		fs, err := pdb.ListFindings(ctx)
		if err == nil {
			out = append(out, fs...)
		}
		return nil
	})
	return out, err
}

// ListAllThreads returns analyst threads across every project.
func (m *Manager) ListAllThreads(ctx context.Context) ([]model.Thread, error) {
	if m.combined {
		return m.global.ListThreads(ctx)
	}
	var out []model.Thread
	err := m.eachProject(ctx, func(pdb *DB) error {
		ts, err := pdb.ListThreads(ctx)
		if err == nil {
			out = append(out, ts...)
		}
		return nil
	})
	return out, err
}

// ListAllPendingApprovals returns pending approvals across every project.
func (m *Manager) ListAllPendingApprovals(ctx context.Context) ([]model.Approval, error) {
	if m.combined {
		return m.global.ListPendingApprovals(ctx)
	}
	var out []model.Approval
	err := m.eachProject(ctx, func(pdb *DB) error {
		as, err := pdb.ListPendingApprovals(ctx)
		if err == nil {
			out = append(out, as...)
		}
		return nil
	})
	return out, err
}

// ListAllTasks returns up to limit tasks across every project (newest-first within each project).
func (m *Manager) ListAllTasks(ctx context.Context, limit int) ([]model.Task, error) {
	if m.combined {
		return m.global.ListTasks(ctx, limit)
	}
	var out []model.Task
	err := m.eachProject(ctx, func(pdb *DB) error {
		ts, err := pdb.ListTasks(ctx, limit)
		if err == nil {
			out = append(out, ts...)
		}
		return nil
	})
	return out, err
}

// ListAllPlaybookRuns returns up to limit playbook runs across every project.
func (m *Manager) ListAllPlaybookRuns(ctx context.Context, limit int) ([]model.PlaybookRun, error) {
	if m.combined {
		return m.global.ListPlaybookRuns(ctx, limit)
	}
	var out []model.PlaybookRun
	err := m.eachProject(ctx, func(pdb *DB) error {
		rs, err := pdb.ListPlaybookRuns(ctx, limit)
		if err == nil {
			out = append(out, rs...)
		}
		return nil
	})
	return out, err
}

// eachProject runs fn against every project's database, for cross-project reads.
func (m *Manager) eachProject(ctx context.Context, fn func(*DB) error) error {
	ids, err := m.activeProjectIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		pdb, err := m.Project(id)
		if err != nil {
			continue
		}
		if err := fn(pdb); err != nil {
			return err
		}
	}
	return nil
}

// --- Cross-database KB ---

// ListKBForProject returns the KB entries visible to a project: its target/group/org anchors (read from
// the project's own database) resolved against the global KB. Combined delegates to the single-database
// query; split reads the anchors from project.db and queries kb_entries in global.db.
func (m *Manager) ListKBForProject(ctx context.Context, projectID string) ([]model.KBEntry, error) {
	if m.combined {
		return m.global.ListKBByProject(ctx, projectID)
	}
	pdb, err := m.Project(projectID)
	if err != nil {
		return nil, err
	}
	proj, err := pdb.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	// Organization anchors: the project's own org plus each linked target's org (targets live in global.db).
	orgs := map[string]bool{}
	if proj.OrganizationID != nil && *proj.OrganizationID != "" {
		orgs[*proj.OrganizationID] = true
	}
	for _, tid := range proj.TargetIDs {
		if t, err := m.global.GetTarget(ctx, tid); err == nil && t.OrganizationID != nil && *t.OrganizationID != "" {
			orgs[*t.OrganizationID] = true
		}
	}
	group := ""
	if proj.GroupID != nil {
		group = *proj.GroupID
	}
	return m.global.ListKBByAnchors(ctx, proj.TargetIDs, group, keys(orgs))
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
