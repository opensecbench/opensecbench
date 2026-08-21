package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

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
	// A custom location must be registered before the project's database is opened, so it lands there.
	// The project's self-contained dir is a subfolder inside the chosen location, so a delete removes only
	// OpenSecBench's files and never the user's sibling source/docs (ADR-0049 containment).
	var projDir string
	if np.Location != "" {
		projDir = filepath.Join(np.Location, ProjectSubdir)
		m.SetProjectDir(id, projDir)
	}
	pdb, err := m.Project(id)
	if err != nil {
		return model.Project{}, err
	}
	p, err := pdb.CreateProjectWithID(ctx, id, np)
	if err != nil {
		return model.Project{}, err
	}
	if err := m.global.UpsertProjectIndex(ctx, p.ID, p.Name, p.Status); err != nil {
		return model.Project{}, err
	}
	if projDir != "" {
		if err := m.global.SetProjectIndexDir(ctx, p.ID, projDir); err != nil {
			return model.Project{}, err
		}
	}
	return p, nil
}

// AdoptProject registers an existing project directory (one that already contains a .opensecbench/
// project.db) in this instance's global index, so a foreign project — cloned, backed up, or created by
// another OSB instance — becomes openable here without re-creating it.
func (m *Manager) AdoptProject(ctx context.Context, location string) (model.Project, error) {
	if m.combined {
		return model.Project{}, fmt.Errorf("store: adopt not supported in combined mode")
	}

	projDir := filepath.Join(location, ProjectSubdir)
	dbPath := filepath.Join(projDir, "project.db")
	if _, err := os.Stat(dbPath); err != nil {
		return model.Project{}, fmt.Errorf("store: no project.db at %s", projDir)
	}

	// Read the project id from the marker (fast, avoids opening the db for just the id).
	// Fall back to reading the projects table if the marker is absent or malformed.
	id := readMarkerID(projDir)
	if id == "" {
		rid, err := readProjectIDFromDB(dbPath)
		if err != nil {
			return model.Project{}, fmt.Errorf("store: cannot determine project id: %w", err)
		}
		id = rid
	}

	// Guard: schema too new — refuse if the project.db has migrations we don't know about.
	tmpDB, err := Open(dbPath)
	if err != nil {
		return model.Project{}, fmt.Errorf("store: open project.db for version check: %w", err)
	}
	ver, err := tmpDB.Version()
	_ = tmpDB.Close()
	if err != nil {
		return model.Project{}, fmt.Errorf("store: read schema version: %w", err)
	}
	if ver > len(m.projMigs) {
		return model.Project{}, fmt.Errorf("store: project schema version %d is newer than this build supports (%d) — upgrade OpenSecBench first", ver, len(m.projMigs))
	}

	// Guard: id collision — if this id is already in the index, it must point to the same dir.
	if known, err := m.projectIndexed(ctx, id); err != nil {
		return model.Project{}, err
	} else if known {
		existing := m.ProjectDir(id)
		if existing == projDir {
			// Already adopted at this location — just open and return it.
			pdb, err := m.Project(id)
			if err != nil {
				return model.Project{}, err
			}
			return pdb.GetProject(ctx, id)
		}
		return model.Project{}, fmt.Errorf("store: a project with id %s is already registered at %s", id, existing)
	}

	// Guard: dir collision — check no other project already claims this directory.
	m.mu.Lock()
	for oid, d := range m.dirs {
		if d == projDir && oid != id {
			m.mu.Unlock()
			return model.Project{}, fmt.Errorf("store: directory %s is already registered to project %s", projDir, oid)
		}
	}
	m.mu.Unlock()

	// Register the custom dir, open+migrate the project database, read the project row.
	m.SetProjectDir(id, projDir)
	pdb, err := m.Project(id)
	if err != nil {
		return model.Project{}, fmt.Errorf("store: open adopted project: %w", err)
	}
	p, err := pdb.GetProject(ctx, id)
	if err != nil {
		return model.Project{}, fmt.Errorf("store: read project from adopted db: %w", err)
	}

	if err := m.global.UpsertProjectIndex(ctx, p.ID, p.Name, p.Status); err != nil {
		return model.Project{}, err
	}
	if err := m.global.SetProjectIndexDir(ctx, p.ID, projDir); err != nil {
		return model.Project{}, err
	}
	return p, nil
}

// readMarkerID reads the project id from a .opensecbench/project.json marker file.
func readMarkerID(projDir string) string {
	b, err := os.ReadFile(filepath.Join(projDir, "project.json"))
	if err != nil {
		return ""
	}
	var m struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.ID
}

// readProjectIDFromDB opens a project.db just enough to read the sole project id.
func readProjectIDFromDB(dbPath string) (string, error) {
	db, err := Open(dbPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	var id string
	err = db.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("no project row in %s: %w", dbPath, err)
	}
	return id, nil
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

// CancelPendingTask marks a still-pending task failed so no worker claims it. Combined operates on the
// one database; split scans projects for the task's owner and cancels it there.
func (m *Manager) CancelPendingTask(ctx context.Context, taskID string) (bool, error) {
	if m.combined {
		return m.global.CancelPendingTask(ctx, taskID)
	}
	ids, err := m.activeProjectIDs(ctx)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		pdb, err := m.Project(id)
		if err != nil {
			continue
		}
		if ok, err := pdb.CancelPendingTask(ctx, taskID); err != nil {
			return false, err
		} else if ok {
			return true, nil
		}
	}
	return false, nil
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

// FailUnfinishedPlans fails agent plans left "running" by a crash/restart, across all projects.
func (m *Manager) FailUnfinishedPlans(ctx context.Context) (int, error) {
	if m.combined {
		return m.global.FailUnfinishedPlans(ctx)
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
		if n, err := pdb.FailUnfinishedPlans(ctx); err == nil {
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

// AggregateUsage sums token usage across the global database and every project database, so the
// cross-project cockpit reflects usage that's recorded into per-project DBs (ADR-0049). Top-model and
// top-agent breakdowns are merged by key and re-sorted to topN.
func (m *Manager) AggregateUsage(ctx context.Context, monthStart time.Time, topN int) (model.UsageSummary, error) {
	if m.combined {
		return m.global.UsageSummary(ctx, monthStart, topN)
	}
	var agg model.UsageSummary
	modelIdx := map[string]int{}
	agentIdx := map[string]int{}
	add := func(s model.UsageSummary) {
		agg.AllInput += s.AllInput
		agg.AllOutput += s.AllOutput
		agg.MonthInput += s.MonthInput
		agg.MonthOutput += s.MonthOutput
		for _, u := range s.TopModels {
			k := u.Provider + "\x00" + u.Model
			if i, ok := modelIdx[k]; ok {
				agg.TopModels[i].Runs += u.Runs
				agg.TopModels[i].InputTokens += u.InputTokens
				agg.TopModels[i].OutputTokens += u.OutputTokens
			} else {
				modelIdx[k] = len(agg.TopModels)
				agg.TopModels = append(agg.TopModels, u)
			}
		}
		for _, a := range s.TopAgents {
			if i, ok := agentIdx[a.AgentType]; ok {
				agg.TopAgents[i].Runs += a.Runs
				agg.TopAgents[i].InputTokens += a.InputTokens
				agg.TopAgents[i].OutputTokens += a.OutputTokens
			} else {
				agentIdx[a.AgentType] = len(agg.TopAgents)
				agg.TopAgents = append(agg.TopAgents, a)
			}
		}
	}
	if gs, err := m.global.UsageSummary(ctx, monthStart, topN); err == nil {
		add(gs) // project-less runs; folded into totals but not a per-project row
	}
	ids, err := m.activeProjectIDs(ctx)
	if err != nil {
		return agg, err
	}
	for _, id := range ids {
		pdb, e := m.Project(id)
		if e != nil {
			continue
		}
		s, e := pdb.UsageSummary(ctx, monthStart, topN)
		if e != nil {
			continue
		}
		add(s)
		if s.AllInput+s.AllOutput > 0 {
			agg.TopProjects = append(agg.TopProjects, model.UsageByProject{ProjectID: id, InputTokens: s.AllInput, OutputTokens: s.AllOutput})
		}
	}
	sort.Slice(agg.TopModels, func(i, j int) bool {
		return agg.TopModels[i].InputTokens+agg.TopModels[i].OutputTokens > agg.TopModels[j].InputTokens+agg.TopModels[j].OutputTokens
	})
	sort.Slice(agg.TopAgents, func(i, j int) bool {
		return agg.TopAgents[i].InputTokens+agg.TopAgents[i].OutputTokens > agg.TopAgents[j].InputTokens+agg.TopAgents[j].OutputTokens
	})
	sort.Slice(agg.TopProjects, func(i, j int) bool {
		return agg.TopProjects[i].InputTokens+agg.TopProjects[i].OutputTokens > agg.TopProjects[j].InputTokens+agg.TopProjects[j].OutputTokens
	})
	if len(agg.TopModels) > topN {
		agg.TopModels = agg.TopModels[:topN]
	}
	if len(agg.TopAgents) > topN {
		agg.TopAgents = agg.TopAgents[:topN]
	}
	if len(agg.TopProjects) > topN {
		agg.TopProjects = agg.TopProjects[:topN]
	}
	return agg, nil
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

// ListAllPlans returns up to limit agent plans (any status) across every project — the durable history
// the Activity feed shows, so a finished plan stays browsable after a restart.
func (m *Manager) ListAllPlans(ctx context.Context, limit int) ([]model.Plan, error) {
	if m.combined {
		return m.global.ListPlans(ctx, limit)
	}
	var out []model.Plan
	err := m.eachProject(ctx, func(pdb *DB) error {
		ps, err := pdb.ListPlans(ctx, limit)
		if err == nil {
			out = append(out, ps...)
		}
		return nil
	})
	return out, err
}

// ListAllRunningPlans returns in-flight agent plans across every project.
func (m *Manager) ListAllRunningPlans(ctx context.Context) ([]model.Plan, error) {
	if m.combined {
		return m.global.ListRunningPlans(ctx)
	}
	var out []model.Plan
	err := m.eachProject(ctx, func(pdb *DB) error {
		ps, err := pdb.ListRunningPlans(ctx)
		if err == nil {
			out = append(out, ps...)
		}
		return nil
	})
	return out, err
}

// ListDueSchedules returns schedules due to run at now, across every project.
func (m *Manager) ListDueSchedules(ctx context.Context, now time.Time) ([]model.Schedule, error) {
	if m.combined {
		return m.global.ListDueSchedules(ctx, now)
	}
	var out []model.Schedule
	err := m.eachProject(ctx, func(pdb *DB) error {
		ss, err := pdb.ListDueSchedules(ctx, now)
		if err == nil {
			out = append(out, ss...)
		}
		return nil
	})
	return out, err
}

// eachProject runs fn against every project's database, for cross-project reads.
// PurgeMethodologyPack removes a deleted methodology pack's per-project traces across every project — the
// adoption row for the pack and the coverage/evidence rows for its items — so deleting a pack from the
// catalog (ADR-0055) leaves no orphaned coverage. The per-project tables key on project_id, so this is safe
// in both combined and split backing (each project's handle sees only its own rows in split mode).
func (m *Manager) PurgeMethodologyPack(ctx context.Context, methodologyID string, itemIDs []string) error {
	ids, err := m.activeProjectIDs(ctx)
	if err != nil {
		return err
	}
	for _, pid := range ids {
		pdb, err := m.Project(pid)
		if err != nil {
			continue
		}
		if err := pdb.UnadoptMethodology(ctx, pid, methodologyID); err != nil {
			return err
		}
		if err := pdb.DeleteCoverageForItems(ctx, pid, itemIDs); err != nil {
			return err
		}
	}
	return nil
}

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
