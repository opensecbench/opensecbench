package cas

import (
	"path/filepath"
	"sync"
)

// Resolver returns the content store that owns a project's blobs. In the two-tier layout (ADR-0049) each
// project keeps its own CAS under projects/<id>/cas, so purge/backup/migrate move a project's evidence with
// it and identical bytes are never shared across projects (isolation over dedup).
type Resolver interface {
	For(projectID string) (*Store, error)
}

// PerProject resolves each project to its own store under <root>/projects/<id>/cas, opening lazily and
// caching handles for the resolver's lifetime.
type PerProject struct {
	root  string
	mu    sync.Mutex
	cache map[string]*Store
}

// NewPerProject creates a resolver rooted at the data directory (the same root the store.Manager uses, so
// projects/<id>/cas sits beside projects/<id>/project.db).
func NewPerProject(root string) *PerProject {
	return &PerProject{root: root, cache: map[string]*Store{}}
}

// For returns the project's store, opening (and creating) it on first use.
func (p *PerProject) For(projectID string) (*Store, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.cache[projectID]; ok {
		return s, nil
	}
	s, err := Open(filepath.Join(p.root, "projects", projectID, "cas"))
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
