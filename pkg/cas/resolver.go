package cas

import "sync"

// Resolver returns the content store that owns a project's blobs. In the two-tier layout (ADR-0049) each
// project keeps its own CAS under projects/<id>/cas, so purge/backup/migrate move a project's evidence with
// it and identical bytes are never shared across projects (isolation over dedup).
type Resolver interface {
	For(projectID string) (*Store, error)
}

// PerProject resolves each project to its own CAS store, opening lazily and caching handles for the
// resolver's lifetime. The per-project directory comes from casDir so evidence sits beside the project's
// database and follows a custom project location (ADR-0049 containment) rather than a hard-coded root.
type PerProject struct {
	casDir func(projectID string) string
	mu     sync.Mutex
	cache  map[string]*Store
}

// NewPerProject creates a resolver whose per-project CAS directory is casDir(projectID) — pass
// store.Manager.ProjectCASDir so projects/<id>/cas sits beside projects/<id>/project.db and a project
// stored at a custom location keeps its evidence there too.
func NewPerProject(casDir func(projectID string) string) *PerProject {
	return &PerProject{casDir: casDir, cache: map[string]*Store{}}
}

// For returns the project's store, opening (and creating) it on first use.
func (p *PerProject) For(projectID string) (*Store, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.cache[projectID]; ok {
		return s, nil
	}
	s, err := Open(p.casDir(projectID))
	if err != nil {
		return nil, err
	}
	p.cache[projectID] = s
	return s, nil
}

// Fixed wraps a single store as a Resolver, ignoring the project id — used by tests and any non-split
// (combined) wiring where one store backs everything.
func Fixed(s *Store) Resolver { return fixed{s} }

type fixed struct{ s *Store }

func (f fixed) For(string) (*Store, error) { return f.s, nil }
