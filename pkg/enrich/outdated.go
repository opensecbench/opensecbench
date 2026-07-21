// Package enrich adds deterministic, third-party-sourced facts to a project's dependency data before any
// agent reasons over it (the "enrich as much as possible first" principle). outdated.go flags dependencies
// that are behind their latest release using Google's deps.dev version index — a currency signal distinct
// from the vulnerability signal that grype/osv-scanner/govulncheck provide.
package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Doer is the subset of *http.Client the deps.dev client needs — injectable so tests run offline.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// depsDevBase is the deps.dev v3 API root; overridable in tests.
const depsDevBase = "https://api.deps.dev/v3"

// Component is one dependency parsed from an SBOM.
type Component struct {
	Ecosystem string // deps.dev system: GO | NPM | PYPI | MAVEN | CARGO | NUGET
	Name      string // deps.dev package name for that system
	Version   string // installed version
}

// Outdated reports a dependency that is behind its latest release.
type Outdated struct {
	Component
	Latest string `json:"latest"`
	Drift  string `json:"drift"` // major | minor | patch
}

// Checker queries deps.dev for the current version of each component.
type Checker struct {
	HTTP    Doer
	BaseURL string
	// Concurrency bounds simultaneous deps.dev requests (default 8).
	Concurrency int
}

// Check returns the components that are behind their latest release. Components whose ecosystem isn't
// supported, that can't be resolved, or that are already current are omitted. Network/lookup errors for a
// single component drop it rather than failing the whole check.
func (c Checker) Check(ctx context.Context, comps []Component) []Outdated {
	base := c.BaseURL
	if base == "" {
		base = depsDevBase
	}
	conc := c.Concurrency
	if conc <= 0 {
		conc = 8
	}
	sem := make(chan struct{}, conc)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var out []Outdated
	for _, comp := range comps {
		if comp.Ecosystem == "" || comp.Name == "" || comp.Version == "" {
			continue
		}
		comp := comp
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			latest, err := c.latestVersion(ctx, base, comp.Ecosystem, comp.Name)
			if err != nil || latest == "" {
				return
			}
			if drift, behind := compareVersions(comp.Version, latest); behind {
				mu.Lock()
				out = append(out, Outdated{Component: comp, Latest: latest, Drift: drift})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type depsDevPackage struct {
	Versions []struct {
		VersionKey struct {
			Version string `json:"version"`
		} `json:"versionKey"`
		IsDefault bool `json:"isDefault"`
	} `json:"versions"`
}

// latestVersion returns the deps.dev "default" (recommended latest) version for a package, falling back to
// the highest version seen.
func (c Checker) latestVersion(ctx context.Context, base, system, name string) (string, error) {
	u := base + "/systems/" + url.PathEscape(system) + "/packages/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("deps.dev %s: status %d", name, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	var pkg depsDevPackage
	if err := json.Unmarshal(body, &pkg); err != nil {
		return "", err
	}
	best := ""
	for _, v := range pkg.Versions {
		ver := v.VersionKey.Version
		if ver == "" || isPrerelease(ver) {
			continue
		}
		if v.IsDefault {
			return ver, nil
		}
		if best == "" {
			best = ver
			continue
		}
		if _, behind := compareVersions(best, ver); behind {
			best = ver
		}
	}
	return best, nil
}

var verRe = regexp.MustCompile(`(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

// compareVersions reports the drift from installed→latest and whether installed is behind. It reads the
// leading numeric major.minor.patch of each (tolerant of a "v" prefix and pre-release/build suffixes),
// which is good enough to answer "is there a newer release, and how big is the jump" across ecosystems.
func compareVersions(installed, latest string) (drift string, behind bool) {
	iv := parseVer(installed)
	lv := parseVer(latest)
	switch {
	case lv[0] > iv[0]:
		return "major", true
	case lv[0] < iv[0]:
		return "", false
	case lv[1] > iv[1]:
		return "minor", true
	case lv[1] < iv[1]:
		return "", false
	case lv[2] > iv[2]:
		return "patch", true
	default:
		return "", false
	}
}

func parseVer(s string) [3]int {
	var out [3]int
	m := verRe.FindStringSubmatch(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	if m == nil {
		return out
	}
	for i := 0; i < 3; i++ {
		if m[i+1] != "" {
			out[i], _ = strconv.Atoi(m[i+1])
		}
	}
	return out
}

// isPrerelease reports whether a version string looks like a pre-release (rc/beta/alpha/dev), which we
// never treat as the "latest stable" to compare against.
func isPrerelease(v string) bool {
	l := strings.ToLower(v)
	for _, tag := range []string{"-rc", "-beta", "-alpha", "-dev", "a", "b", "rc", ".dev"} {
		if strings.Contains(l, tag) && strings.ContainsAny(l, "-.") {
			// crude but safe: only flag when a known pre-release token appears with a separator
			if strings.Contains(l, "-"+strings.TrimPrefix(tag, "-")) || strings.Contains(l, ".dev") {
				return true
			}
		}
	}
	return false
}

var purlTypeToSystem = map[string]string{
	"pypi":   "PYPI",
	"npm":    "NPM",
	"golang": "GO",
	"cargo":  "CARGO",
	"maven":  "MAVEN",
	"nuget":  "NUGET",
}

// ComponentsFromPURLs maps package URLs (as syft emits in a CycloneDX SBOM) to deps.dev components,
// dropping any whose type deps.dev doesn't index.
func ComponentsFromPURLs(purls []string) []Component {
	var out []Component
	for _, p := range purls {
		if c, ok := componentFromPURL(p); ok {
			out = append(out, c)
		}
	}
	return out
}

// componentFromPURL parses a single purl like "pkg:pypi/flask@2.0.1" or "pkg:maven/org.group/artifact@1.0"
// or "pkg:golang/golang.org%2Fx%2Fnet@v0.4.0" into a deps.dev component.
func componentFromPURL(purl string) (Component, bool) {
	s := strings.TrimPrefix(purl, "pkg:")
	if s == purl {
		return Component{}, false
	}
	// Strip any qualifiers/subpath.
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return Component{}, false
	}
	version := s[at+1:]
	head := s[:at]
	slash := strings.Index(head, "/")
	if slash < 0 {
		return Component{}, false
	}
	typ := strings.ToLower(head[:slash])
	rest := head[slash+1:]
	system, ok := purlTypeToSystem[typ]
	if !ok {
		return Component{}, false
	}
	name := decodePURLName(rest)
	// Maven names are "group:artifact"; purl carries them as "group/artifact".
	if system == "MAVEN" {
		name = strings.Replace(name, "/", ":", 1)
	}
	if version, err := url.PathUnescape(version); err == nil {
		return Component{Ecosystem: system, Name: name, Version: version}, name != ""
	}
	return Component{Ecosystem: system, Name: name, Version: version}, name != ""
}

func decodePURLName(s string) string {
	if d, err := url.PathUnescape(s); err == nil {
		return d
	}
	return s
}
