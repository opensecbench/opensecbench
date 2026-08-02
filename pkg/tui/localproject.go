package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// A dir-local project (ADR-0063/0049): running `osb tui` in a directory can create a project whose data
// (project.db + cas + workspace) lives in a .opensecbench/ folder there, and a marker binds the directory
// to that project so a later launch re-opens it. The user's global instance still holds settings and
// provider connections, so the Analyst works immediately without per-dir setup.

const (
	projectDirName = ".opensecbench"
	projectMarker  = "project.json"
)

// Options configure a TUI session with its working-directory context.
type Options struct {
	Cwd           string // where `osb tui` was launched (offers "create a project here")
	OpenProjectID string // a project bound to this dir via the marker; auto-opened when set
}

type marker struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// DiscoverProject walks up from startDir looking for a .opensecbench/project.json marker, returning the
// bound project id. Empty when none is found — like git discovering .git up the tree.
func DiscoverProject(startDir string) string {
	dir := startDir
	for {
		if b, err := os.ReadFile(filepath.Join(dir, projectDirName, projectMarker)); err == nil {
			var m marker
			if json.Unmarshal(b, &m) == nil && m.ID != "" {
				return m.ID
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the filesystem root
			return ""
		}
		dir = parent
	}
}

// writeMarker binds cwd to a project by recording its id in cwd/.opensecbench/project.json, so a later
// launch here re-opens it. Best-effort: a marker failure doesn't undo a created project.
func writeMarker(cwd, id, name string) error {
	dir := filepath.Join(cwd, projectDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(marker{ID: id, Name: name}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, projectMarker), b, 0o644)
}
