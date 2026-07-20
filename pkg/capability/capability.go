// Package capability defines the typed security operations that users and agents invoke
// (ADR-0003). A capability turns typed input into a sandboxed RunSpec (ADR-0004); the task
// engine runs it and captures its output as an artifact with provenance.
package capability

import (
	"sort"
	"sync"

	"github.com/opensecbench/opensecbench/pkg/disposition"
	"github.com/opensecbench/opensecbench/pkg/runner"
)

// Manifest describes a capability.
type Manifest struct {
	ID              string `json:"id"`
	Version         string `json:"version"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	OutputName      string `json:"output_name"`       // logical name of the primary output artifact
	OutputMediaType string `json:"output_media_type"` //
	// OKExitCodes lists exit codes that count as success. Empty means only 0.
	OKExitCodes []int `json:"ok_exit_codes,omitempty"`
	// TargetParam names the input param holding a network target (host/URL). When set, the
	// capability touches the network and the engine enforces the scope allowlist against it.
	TargetParam string `json:"target_param,omitempty"`
	// Technique tags this capability with a rules-of-engagement technique (ADR-0051): one of
	// "intrusive", "automated_exploit", "brute_force", "dos", "social", "destructive". When set, the engine
	// blocks the run unless the project's engagement permits that technique. Empty = passive, always allowed.
	Technique string `json:"technique,omitempty"`
	// Dispositions route this capability's observations to a post-run action (ADR-0028). Empty means
	// everything is left for manual review (default).
	Dispositions []disposition.Disposition `json:"dispositions,omitempty"`
}

// ExitOK reports whether an exit code counts as a successful run.
func (m Manifest) ExitOK(code int) bool {
	if len(m.OKExitCodes) == 0 {
		return code == 0
	}
	for _, c := range m.OKExitCodes {
		if c == code {
			return true
		}
	}
	return false
}

// Input is what a capability plans against.
type Input struct {
	TargetDir string // host path mounted read-only into the sandbox
	Params    map[string]any
}

// Capability is a typed operation that plans a sandboxed execution.
type Capability interface {
	Manifest() Manifest
	Plan(in Input) (runner.RunSpec, error)
}

// Registry holds the available capabilities by id. It is safe for concurrent use so extensions can
// be registered at runtime (hub install) while tasks read it.
type Registry struct {
	mu   sync.RWMutex
	caps map[string]Capability
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{caps: make(map[string]Capability)} }

// Register adds a capability (replacing any with the same id).
func (r *Registry) Register(c Capability) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.caps[c.Manifest().ID] = c
}

// Get returns a capability by id.
func (r *Registry) Get(id string) (Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.caps[id]
	return c, ok
}

// Manifests returns all capability manifests sorted by id.
func (r *Registry) Manifests() []Manifest {
	r.mu.RLock()
	out := make([]Manifest, 0, len(r.caps))
	for _, c := range r.caps {
		out = append(out, c.Manifest())
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
