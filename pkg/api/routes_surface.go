package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// routeFinding is a vulnerability reachable from (or co-located in) a route's handler.
type routeFinding struct {
	ObservationID  string `json:"observation_id"`
	Title          string `json:"title"`
	Severity       string `json:"severity"`
	RouteReachable bool   `json:"route_reachable"` // a proven dataflow path from this route to the sink
}

// routeView is one entry point plus the risk behind it — the attack-surface view (ADR-0033/0034): what
// endpoints exist, which are traffic-confirmed, and which findings are reachable from each.
type routeView struct {
	model.Route
	Findings       []routeFinding `json:"findings"`
	WorstSeverity  string         `json:"worst_severity,omitempty"`
	ReachableCount int            `json:"reachable_count"` // findings with a proven route→sink path
}

// listProjectRoutes serves the attack surface: the route inventory joined with the findings each route
// exposes, ranked so the most dangerous entry points come first.
func (s *Server) listProjectRoutes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	routes, err := s.pdb(r).ListRoutesByProject(ctx, projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	obs, _ := s.pdb(r).ListObservationsByProject(ctx, projectID)
	// Index findings by the route they were correlated to (exposed_route = "METHOD PATH").
	byRoute := map[string][]routeFinding{}
	for _, o := range obs {
		key := strings.TrimSpace(o.Attributes["exposed_route"])
		if key == "" {
			continue
		}
		byRoute[key] = append(byRoute[key], routeFinding{
			ObservationID:  o.ID,
			Title:          o.Title,
			Severity:       o.Severity,
			RouteReachable: o.Attributes["route_reachable"] == "true",
		})
	}

	views := make([]routeView, 0, len(routes))
	for _, rt := range routes {
		v := routeView{Route: rt}
		key := strings.TrimSpace(rt.Method + " " + rt.Path)
		v.Findings = byRoute[key]
		for _, f := range v.Findings {
			if f.RouteReachable {
				v.ReachableCount++
			}
			if sevRank(f.Severity) > sevRank(v.WorstSeverity) {
				v.WorstSeverity = f.Severity
			}
		}
		// Reachable findings first within a route, then by severity.
		sort.SliceStable(v.Findings, func(i, j int) bool {
			if v.Findings[i].RouteReachable != v.Findings[j].RouteReachable {
				return v.Findings[i].RouteReachable
			}
			return sevRank(v.Findings[i].Severity) > sevRank(v.Findings[j].Severity)
		})
		views = append(views, v)
	}
	// Rank routes: worst severity, then reachable count, then traffic-confirmed, then path.
	sort.SliceStable(views, func(i, j int) bool {
		if a, b := sevRank(views[i].WorstSeverity), sevRank(views[j].WorstSeverity); a != b {
			return a > b
		}
		if views[i].ReachableCount != views[j].ReachableCount {
			return views[i].ReachableCount > views[j].ReachableCount
		}
		if views[i].Observed != views[j].Observed {
			return views[i].Observed
		}
		return views[i].Path < views[j].Path
	})
	writeJSON(w, http.StatusOK, views)
}

func sevRank(s string) int { return sevOrder[s] }

// listReachability returns reachability facts. With subject_type + subject query params, it also resolves
// the effective verdict for that subject; otherwise it lists all of the project's facts.
func (s *Server) listReachability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	st := r.URL.Query().Get("subject_type")
	sk := r.URL.Query().Get("subject")
	if st != "" && sk != "" {
		verdict, confidence, facts := s.pdb(r).ResolveReachability(ctx, projectID, st, sk)
		writeJSON(w, http.StatusOK, map[string]any{
			"reachable": verdict, "confidence": confidence, "facts": facts,
		})
		return
	}
	facts, err := s.pdb(r).ListReachabilityFactsByProject(ctx, projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, facts)
}

// addReachability records a manual reachability determination (source=manual) — a human's verdict on
// whether a finding/CVE is reachable, aggregated with the tool and LLM facts.
func (s *Server) addReachability(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SubjectType string `json:"subject_type"`
		Subject     string `json:"subject"`
		Reachable   string `json:"reachable"`
		Confidence  string `json:"confidence"`
		Rationale   string `json:"rationale"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Subject == "" || req.Reachable == "" {
		writeErr(w, http.StatusBadRequest, "subject and reachable are required")
		return
	}
	projectID := r.PathValue("id")
	if err := s.pdb(r).AddReachabilityFact(r.Context(), model.ReachabilityFact{
		ProjectID: projectID, SubjectType: req.SubjectType, SubjectKey: req.Subject,
		Reachable: req.Reachable, Confidence: req.Confidence, Source: "manual",
		Method: "manual review", Rationale: req.Rationale, Actor: actorOf(r),
	}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "reachability.manual", req.Subject, map[string]any{"reachable": req.Reachable})
	verdict, confidence, facts := s.pdb(r).ResolveReachability(r.Context(), projectID, req.SubjectType, req.Subject)
	writeJSON(w, http.StatusOK, map[string]any{"reachable": verdict, "confidence": confidence, "facts": facts})
}
