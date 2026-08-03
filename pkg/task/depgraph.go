package task

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// depGraph classifies dependencies as direct/transitive with the path to a transitive one, from a syft
// CycloneDX SBOM (ADR-0069): a transitive CVE is fixed by upgrading its direct parent, not the leaf.
type depGraph struct {
	class map[string]string   // coordinate (lower) -> "direct" | "transitive"
	path  map[string][]string // coordinate (lower) -> chain of coordinates from a direct dep down to it
}

type cdxSBOM struct {
	Metadata struct {
		Component struct {
			BOMRef string `json:"bom-ref"`
		} `json:"component"`
	} `json:"metadata"`
	Components []struct {
		BOMRef string `json:"bom-ref"`
		Name   string `json:"name"`
		Purl   string `json:"purl"`
	} `json:"components"`
	Dependencies []struct {
		Ref       string   `json:"ref"`
		DependsOn []string `json:"dependsOn"`
	} `json:"dependencies"`
}

// buildDepGraph returns nil for an empty/unparseable SBOM so callers skip correlation.
func buildDepGraph(raw []byte) *depGraph {
	var s cdxSBOM
	if err := json.Unmarshal(raw, &s); err != nil || len(s.Components) == 0 {
		return nil
	}
	root := s.Metadata.Component.BOMRef
	coordOf := map[string]string{}
	for _, c := range s.Components {
		ref := c.BOMRef
		if ref == "" {
			ref = c.Name
		}
		coord := purlCoordinate(c.Purl)
		if coord == "" {
			coord = c.Name
		}
		if coord != "" {
			coordOf[ref] = strings.ToLower(coord)
		}
	}
	adj := map[string][]string{}
	for _, d := range s.Dependencies {
		adj[d.Ref] = d.DependsOn
	}

	dist := map[string]int{root: 0}
	pred := map[string]string{}
	for q := []string{root}; len(q) > 0; {
		cur := q[0]
		q = q[1:]
		for _, ch := range adj[cur] {
			if _, seen := dist[ch]; seen {
				continue
			}
			dist[ch] = dist[cur] + 1
			pred[ch] = cur
			q = append(q, ch)
		}
	}

	g := &depGraph{class: map[string]string{}, path: map[string][]string{}}
	for ref, coord := range coordOf {
		if ref == root || coord == "" {
			continue
		}
		d, linked := dist[ref]
		if linked && d == 1 {
			g.class[coord] = "direct"
		} else {
			g.class[coord] = "transitive" // deeper, or present but not linked from root (shallow SBOM)
		}
		if linked {
			var trail []string
			for x := ref; x != root && x != ""; x = pred[x] {
				if c := coordOf[x]; c != "" {
					trail = append([]string{c}, trail...)
				}
			}
			g.path[coord] = trail
		}
	}
	return g
}

// purlCoordinate reduces a Package URL to the coordinate SCA findings use: maven -> "group:artifact",
// otherwise "namespace/name".
func purlCoordinate(purl string) string {
	s := strings.TrimPrefix(purl, "pkg:")
	if i := strings.IndexAny(s, "@?#"); i >= 0 {
		s = s[:i]
	}
	typ, rest, ok := strings.Cut(s, "/")
	if !ok || rest == "" {
		return ""
	}
	if typ == "maven" {
		if i := strings.LastIndex(rest, "/"); i >= 0 {
			return rest[:i] + ":" + rest[i+1:]
		}
	}
	return rest
}

func correlateDependency(o *model.Observation, g *depGraph) {
	if g == nil || o.Attributes == nil || o.Attributes["dependency"] != "" {
		return
	}
	pkg := strings.ToLower(o.Attributes["package"])
	cls, ok := g.class[pkg]
	if pkg == "" || !ok {
		return
	}
	o.Attributes["dependency"] = cls
	if p := g.path[pkg]; len(p) > 1 {
		o.Attributes["dependency_path"] = strings.Join(p, " → ")
	}
}

func (e *Engine) loadDepGraph(ctx context.Context, projectID string) *depGraph {
	sha, err := e.p(projectID).LatestArtifactSHA(ctx, projectID, "syft")
	if err != nil {
		return nil
	}
	st := e.casFor(projectID)
	if st == nil {
		return nil
	}
	rc, err := st.Open(sha)
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	return buildDepGraph(raw)
}
