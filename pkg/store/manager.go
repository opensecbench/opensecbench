package store

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Manager owns the two-tier storage layout under a root directory (ADR-0049): a single instance-wide
// global.db plus an on-demand pool of per-project databases, each living in its own directory alongside
// that project's CAS and workspace. It is the authority on the on-disk layout, so purge/backup/migrate
// operate on one self-contained directory per project.
type Manager struct {
	root       string
	global     *DB
	projMigs   []Migration
	globalMigs []Migration

	// combined backs both domains with a single database that holds every table (ADR-0049 phase 2a).
	// It lets call sites migrate to the two-tier Manager API while behavior stays identical to the
	// legacy single-DB layout; phase 2b replaces it with the real per-project backing.
	combined bool

	mu       sync.Mutex
	projects map[string]*DB    // lazily opened per-project handles, keyed by project id
	dirs     map[string]string // custom on-disk locations, keyed by project id (empty/absent = default)
}

// NewCombinedManager wraps a single full-schema database as a Manager whose Global() and Project() both
// return it (ADR-0049 phase 2a). Used by tests and the transitional control-plane wiring so the two-tier
// API can be adopted before the physical split lands.
func NewCombinedManager(db *DB) *Manager {
	return &Manager{global: db, combined: true, projects: map[string]*DB{}, dirs: map[string]string{}}
}

// OpenCombined opens a single database at path, applies both schema sets to it (their tables are
// disjoint), and returns a combined-mode Manager. Transitional (ADR-0049 phase 2a).
func OpenCombined(path string, globalFS, projectFS fs.FS) (*Manager, error) {
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	for _, f := range []fs.FS{globalFS, projectFS} {
		migs, err := LoadMigrations(f)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		for _, m := range migs {
			if _, err := db.Exec(m.SQL); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("store: combined schema %s: %w", m.Name, err)
			}
		}
	}
	return NewCombinedManager(db), nil
}

// OpenManager opens (creating if needed) the storage tree rooted at dir. It opens and migrates global.db
// eagerly; per-project databases are opened on first use. globalFS and projectFS are the two migration
// sets (see migrations.Global()/Project()).
func OpenManager(dir string, globalFS, projectFS fs.FS) (*Manager, error) {
	globalMigs, err := LoadMigrations(globalFS)
	if err != nil {
		return nil, fmt.Errorf("store: load global migrations: %w", err)
	}
	projMigs, err := LoadMigrations(projectFS)
	if err != nil {
		return nil, fmt.Errorf("store: load project migrations: %w", err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	global, err := openMigrated(filepath.Join(dir, "global.db"), globalMigs)
	if err != nil {
		return nil, fmt.Errorf("store: open global.db: %w", err)
	}
	// Custom project locations persisted in the index (ADR-0049 containment) must be known before a
	// project's database is opened, so load them up front.
	dirs, err := global.ProjectIndexDirs(context.Background())
	if err != nil {
		_ = global.Close()
		return nil, fmt.Errorf("store: load project locations: %w", err)
	}
	return &Manager{
		root:       dir,
		global:     global,
		globalMigs: globalMigs,
		projMigs:   projMigs,
		projects:   map[string]*DB{},
		dirs:       dirs,
	}, nil
}

// Global returns the instance-wide database handle (org tree, targets, KB, settings, providers, runners,
// secrets, audit, and the project index).
func (m *Manager) Global() *DB { return m.global }

// Root is the storage tree root directory.
func (m *Manager) Root() string { return m.root }

// ProjectSubdir is the folder OpenSecBench creates inside a user-designated project location to hold the
// self-contained project (project.db + cas/ + workspace/), so its files stay in one clearly-owned place
// alongside the user's own source/docs and a project delete removes only this folder.
const ProjectSubdir = ".opensecbench"

// ProjectDir is the self-contained directory for a project: project.db + cas/ + workspace/ live here, so
// it is the unit of purge, backup, and migrate. It is a custom location if one was set at creation
// (ADR-0049 containment), otherwise the default <root>/projects/<id>.
func (m *Manager) ProjectDir(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectDirLocked(id)
}

// projectDirLocked resolves the project directory; the caller must hold m.mu.
func (m *Manager) projectDirLocked(id string) string {
	if d := m.dirs[id]; d != "" {
		return d
	}
	return filepath.Join(m.root, "projects", id)
}

// ProjectCASDir and ProjectWorkspaceDir name the sibling stores inside a project's directory. The Manager
// owns the layout; callers (e.g. the CAS opener) build on these instead of hard-coding paths.
func (m *Manager) ProjectCASDir(id string) string { return filepath.Join(m.ProjectDir(id), "cas") }
func (m *Manager) ProjectWorkspaceDir(id string) string {
	return filepath.Join(m.ProjectDir(id), "workspace")
}

// Project returns the opened, migrated handle for a project's project.db, opening it on first use. Handles
// are cached for the life of the Manager.
func (m *Manager) Project(id string) (*DB, error) {
	if m.combined {
		return m.global, nil
	}
	if id == "" {
		return nil, fmt.Errorf("store: empty project id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := m.projects[id]; ok {
		return db, nil
	}
	dir := m.projectDirLocked(id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	db, err := openMigrated(filepath.Join(dir, "project.db"), m.projMigs)
	if err != nil {
		return nil, fmt.Errorf("store: open project %s: %w", id, err)
	}
	m.projects[id] = db
	return db, nil
}

// SetProjectDir registers a custom on-disk location for a project before its database is first opened.
// dir is the project's self-contained directory (project.db + cas/ + workspace/ go inside). No-op for "".
func (m *Manager) SetProjectDir(id, dir string) {
	if dir == "" {
		return
	}
	m.mu.Lock()
	m.dirs[id] = dir
	m.mu.Unlock()
}

// PurgeProject closes the project's open handle (if any), removes its entire directory (project.db + cas +
// workspace in one shot — no orphans), and drops its row from the global project index.
func (m *Manager) PurgeProject(ctx context.Context, id string) error {
	m.mu.Lock()
	if db, ok := m.projects[id]; ok {
		_ = db.Close()
		delete(m.projects, id)
	}
	m.mu.Unlock()

	if err := os.RemoveAll(m.ProjectDir(id)); err != nil {
		return fmt.Errorf("store: remove project dir: %w", err)
	}
	return m.global.DeleteProjectIndex(ctx, id)
}

// Close closes every open project handle and the global handle.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for id, db := range m.projects {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.projects, id)
	}
	if err := m.global.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// openMigrated opens a database at path and applies the given migration set.
func openMigrated(path string, migs []Migration) (*DB, error) {
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Apply(migs); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// ProjectIndexRow is one entry in the global directory of projects.
type ProjectIndexRow struct {
	ID        string
	Name      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpsertProjectIndex records (or updates) a project in the global directory. Called on project create and
// on name/status change so cross-project listing never has to open every per-project database.
func (db *DB) UpsertProjectIndex(ctx context.Context, id, name, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx,
		`INSERT INTO project_index (id, name, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, status = excluded.status, updated_at = excluded.updated_at`,
		id, name, status, now, now)
	return err
}

// SetProjectIndexDir persists a project's custom on-disk location (ADR-0049 containment). Empty clears it.
func (db *DB) SetProjectIndexDir(ctx context.Context, id, dir string) error {
	_, err := db.ExecContext(ctx, `UPDATE project_index SET dir = ? WHERE id = ?`, dir, id)
	return err
}

// ProjectIndexDirs returns the custom on-disk location for every project that has one (id -> dir). Loaded
// by the Manager at startup so a project's database opens at its designated path.
func (db *DB) ProjectIndexDirs(ctx context.Context) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, dir FROM project_index WHERE dir != ''`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, dir string
		if err := rows.Scan(&id, &dir); err != nil {
			return nil, err
		}
		out[id] = dir
	}
	return out, rows.Err()
}

// DeleteProjectIndex removes a project from the global directory (part of purge).
func (db *DB) DeleteProjectIndex(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM project_index WHERE id = ?`, id)
	return err
}

// ListProjectIndex returns every project in the global directory, newest first.
func (db *DB) ListProjectIndex(ctx context.Context) ([]ProjectIndexRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, status, created_at, updated_at FROM project_index ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ProjectIndexRow
	for rows.Next() {
		var r ProjectIndexRow
		var created, updated string
		if err := rows.Scan(&r.ID, &r.Name, &r.Status, &created, &updated); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		out = append(out, r)
	}
	return out, rows.Err()
}
