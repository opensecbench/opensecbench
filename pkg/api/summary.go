package api

import (
	"io"
	"net/http"
	"strings"

	"encoding/json"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// projectSummary is the at-a-glance rollup of what an assessment has found — the payoff of the scan →
// triage → reachability pipeline, for the Overview.
type projectSummary struct {
	Findings           map[string]int `json:"findings"` // severity → count, plus "total" and "open"
	Reachable          int            `json:"reachable"`
	OpenInvestigations int            `json:"open_investigations"`
	// Queue is the triage backlog (ADR-0068): unreviewed observations not yet under an investigation — the
	// primary triage motion. HighOrCritical / Reachable help gauge what to work first.
	Queue struct {
		Total, HighOrCritical, Reachable int
	} `json:"queue"`
	Routes struct {
		Total, Exposed, WithFindings int
	} `json:"routes"`
	Dependencies struct {
		Total, Vulnerabilities, Outdated int
	} `json:"dependencies"`
}

func (s *Server) projectSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	sum := projectSummary{Findings: map[string]int{}}

	findings, _ := s.pdb(r).ListFindings(ctx)
	for _, f := range findings {
		sum.Findings[f.Severity]++
		sum.Findings["total"]++
		if f.Status == model.FindingOpen {
			sum.Findings["open"]++
		}
	}

	// An observation is in the triage Queue when it is unreviewed and not already under an active
	// investigation — so gather the active-investigation observation set first.
	invs, _ := s.pdb(r).ListInvestigationsByProject(ctx, projectID)
	activeInvObs := map[string]bool{}
	for _, iv := range invs {
		if iv.Status == model.InvestigationOpen {
			sum.OpenInvestigations++
		}
		if iv.Status == model.InvestigationOpen || iv.Status == model.InvestigationInvestigating {
			activeInvObs[iv.ObservationID] = true
		}
	}

	obs, _ := s.pdb(r).ListObservationsByProject(ctx, projectID)
	routeWithFinding := map[string]bool{}
	for _, o := range obs {
		if o.Attributes["reachable"] == "true" {
			sum.Reachable++
		}
		if o.Attributes["outdated"] == "true" {
			sum.Dependencies.Outdated++
		} else if looksLikeVuln(o) {
			sum.Dependencies.Vulnerabilities++
		}
		if k := strings.TrimSpace(o.Attributes["exposed_route"]); k != "" {
			routeWithFinding[k] = true
		}
		if o.ReviewState == model.ReviewUnreviewed && !activeInvObs[o.ID] {
			sum.Queue.Total++
			if o.Severity == "critical" || o.Severity == "high" {
				sum.Queue.HighOrCritical++
			}
			if o.Attributes["reachable"] == "true" || o.Attributes["reachable_confirmed"] == "true" || o.Attributes["route_reachable"] == "true" {
				sum.Queue.Reachable++
			}
		}
	}

	routes, _ := s.pdb(r).ListRoutesByProject(ctx, projectID)
	sum.Routes.Total = len(routes)
	for _, rt := range routes {
		if rt.Observed {
			sum.Routes.Exposed++
		}
		if routeWithFinding[strings.TrimSpace(rt.Method+" "+rt.Path)] {
			sum.Routes.WithFindings++
		}
	}

	sum.Dependencies.Total = s.sbomComponentCount(r, projectID)
	writeJSON(w, http.StatusOK, sum)
}

// sbomComponentCount returns how many components the project's latest syft SBOM lists (0 if none yet).
func (s *Server) sbomComponentCount(r *http.Request, projectID string) int {
	sha, err := s.pdb(r).LatestArtifactSHA(r.Context(), projectID, "syft")
	if err != nil {
		return 0
	}
	rc, err := s.casFor(projectID).Open(sha)
	if err != nil {
		return 0
	}
	defer func() { _ = rc.Close() }()
	raw, _ := io.ReadAll(rc)
	var sbom cycloneDX
	if json.Unmarshal(raw, &sbom) != nil {
		return 0
	}
	return len(sbom.Components)
}
