package capability

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// EcosystemMarkers maps a stack name to the manifest/lockfiles that signal its presence. Detection is the
// input to the scan auto-run gate; an operator can also tag an asset manually to correct it.
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

// markerToEcosystem inverts EcosystemMarkers to a filename → ecosystem lookup, built once.
var markerToEcosystem = func() map[string]string {
	m := map[string]string{}
	for eco, markers := range EcosystemMarkers {
		for _, f := range markers {
			m[f] = eco
		}
	}
	return m
}()

// ecoSkipDirs are noise directories never worth walking for stack markers (deps, build output, VCS).
var ecoSkipDirs = map[string]bool{
	"node_modules": true, ".git": true, "vendor": true, "target": true, "dist": true,
	"build": true, ".venv": true, "venv": true, "__pycache__": true, ".tox": true, ".gradle": true,
}

// ecoMaxDepth bounds how deep detection walks — enough to catch a monorepo's per-service manifests
// (services/api/go.mod, packages/web/package.json) without traversing a whole tree.
const ecoMaxDepth = 4

// DetectEcosystems returns the set of language ecosystems present in a repo, found by a bounded, noise-
// skipping walk for manifest/lock markers — so a monorepo with nested manifests is detected, not just the
// root. Best-effort: an unreadable/empty dir yields the empty set.
func DetectEcosystems(dir string) map[string]bool {
	out := map[string]bool{}
	if dir == "" {
		return out
	}
	root := filepath.Clean(dir)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry — skip, don't abort the walk
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if ecoSkipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			if depth(root, path) >= ecoMaxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if eco, ok := markerToEcosystem[d.Name()]; ok {
			out[eco] = true
		}
		return nil
	})
	return out
}

func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator))
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
