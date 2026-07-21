package capability

import (
	"os"
	"path/filepath"
)

// EcosystemMarkers maps a stack name to the files that signal its presence at a repo root. Detection is
// the input to the scan auto-run gate; an operator can also tag an asset manually to correct it.
var EcosystemMarkers = map[string][]string{
	"go":     {"go.mod"},
	"node":   {"package.json"},
	"python": {"requirements.txt", "pyproject.toml", "setup.py", "Pipfile"},
	"rust":   {"Cargo.toml"},
	"ruby":   {"Gemfile"},
	"java":   {"pom.xml", "build.gradle"},
	"php":    {"composer.json"},
	"dotnet": {"packages.config"},
}

// DetectEcosystems returns the set of language ecosystems present at a repo root, from marker files.
// Best-effort: an unreadable/empty dir yields the empty set.
func DetectEcosystems(dir string) map[string]bool {
	out := map[string]bool{}
	if dir == "" {
		return out
	}
	for eco, markers := range EcosystemMarkers {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				out[eco] = true
				break
			}
		}
	}
	return out
}

// DetectEcosystemList returns DetectEcosystems as a slice (for display/serialization).
func DetectEcosystemList(dir string) []string {
	m := DetectEcosystems(dir)
	out := make([]string, 0, len(m))
	for e := range m {
		out = append(out, e)
	}
	return out
}
