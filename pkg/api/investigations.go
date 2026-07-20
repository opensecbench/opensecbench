package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/disposition"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// describeSignals renders an observation's routing attributes (ADR-0029–0034) into a compact block for the
// investigation seed, so the vuln-validator knows *why* this was flagged and where to look — its
// reachability, exposure, exposed route, and CVSS. Returns "" when there is nothing worth stating.
func describeSignals(attrs map[string]string) string {
	if len(attrs) == 0 {
		return ""
	}
	var lines []string
	if v := attrs["reachable"]; v == "true" {
		if src := attrs["dataflow_source"]; src != "" {
			lines = append(lines, "Reachable: yes — untrusted input reaches this from "+src)
		} else {
			lines = append(lines, "Reachable: yes (in the call graph)")
		}
	} else if v == "false" {
		lines = append(lines, "Reachable: no — the vulnerable code is imported but not called (likely a false positive)")
	}
	if route := attrs["exposed_route"]; route != "" {
		confirm := "declared"
		if attrs["route_observed"] == "true" {
			confirm = "traffic-confirmed"
		}
		lines = append(lines, fmt.Sprintf("Exposed route: %s (%s)", route, confirm))
	} else if attrs["exposed"] == "true" {
		lines = append(lines, "On a network-exposed service")
	}
	if cvss := attrs["security_severity"]; cvss != "" {
		lines = append(lines, "CVSS: "+cvss)
	}
	if attrs["verified"] == "true" {
		lines = append(lines, "Secret verified live by the tool")
	}
	if pkg := attrs["package"]; pkg != "" {
		s := "Package: " + pkg
		if fixed := attrs["fixed_version"]; fixed != "" {
			s += " (fixed in " + fixed + ")"
		}
		lines = append(lines, s)
	}
	if len(lines) == 0 {
		return ""
	}
	return "Signals:\n- " + strings.Join(lines, "\n- ") + "\n"
}

// listProjectObservations returns a project's observations with their routing attributes (ADR-0037), for
// triage UIs and tooling. Each observation is enriched with the source_repo `asset_id` its location refers
// to (ADR-0050), so the UI can offer click-to-file. `?unreviewed_only=true` narrows to observations still
// awaiting triage.
func (s *Server) listProjectObservations(w http.ResponseWriter, r *http.Request) {
	obs, err := s.pdb(r).ListLocatedObservationsByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if r.URL.Query().Get("unreviewed_only") == "true" {
		filtered := obs[:0]
		for _, o := range obs {
			if o.ReviewState == model.ReviewUnreviewed {
				filtered = append(filtered, o)
			}
		}
		obs = filtered
	}
	writeJSON(w, http.StatusOK, obs)
}

// listInvestigations returns a project's investigations (ADR-0028).
func (s *Server) listInvestigations(w http.ResponseWriter, r *http.Request) {
	inv, err := s.pdb(r).ListInvestigationsByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// setInvestigationStatus resolves or dismisses an investigation.
func (s *Server) setInvestigationStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	switch req.Status {
	case model.InvestigationOpen, model.InvestigationResolved, model.InvestigationDismissed:
	default:
		writeErr(w, http.StatusBadRequest, "status must be open, resolved or dismissed")
		return
	}
	if err := s.pdb(r).SetInvestigationStatus(r.Context(), r.PathValue("id"), req.Status); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "investigation not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "investigation."+req.Status, r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

// runInvestigation starts a vuln-validator agent thread seeded with the observation to validate it
// (ADR-0028). Any finding the agent proposes is human-gated; the human stays in the loop.
func (s *Server) runInvestigation(w http.ResponseWriter, r *http.Request) {
	if s.llmProvider() == nil {
		writeErr(w, http.StatusServiceUnavailable, "no LLM provider configured")
		return
	}
	inv, err := s.pdb(r).GetInvestigation(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "investigation not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	obs, err := s.pdb(r).GetObservation(r.Context(), inv.ObservationID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	th, err := s.pdb(r).CreateThread(r.Context(), store.NewThread{
		ProjectID: &inv.ProjectID, Title: "Investigate: " + inv.Title,
		AgentType: "vuln-validator", Provider: s.providerName(),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	seed := fmt.Sprintf(
		"Validate whether this potential issue is real, then decide.\n\n"+
			"Rule: %s\nSeverity: %s\nLocation: %s\nDetail: %s\n%s\n"+
			"Gather evidence (read the relevant code/context; test only what is safe and in scope). "+
			"If it is a genuine issue, create a finding — that will require my approval. "+
			"If it is a false positive or example/placeholder value, explain why.",
		obs.RuleID, obs.Severity, obs.Location, obs.Detail, describeSignals(obs.Attributes))

	res, err := s.analystService().Send(r.Context(), projectFromReq(r), th.ID, seed)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "start investigation: "+err.Error())
		return
	}
	if err := s.pdb(r).SetInvestigationThread(r.Context(), inv.ID, th.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordUsage(r.Context(), res)
	s.notifyIfPending(r.Context(), res)
	s.record(r.Context(), actorOf(r), "investigation.run", inv.ID, map[string]string{"thread": th.ID})
	writeJSON(w, http.StatusOK, map[string]any{"investigation_id": inv.ID, "thread": res.Thread, "result": res})
}

// listDispositions returns a project's disposition overrides plus each capability's manifest-declared
// defaults (ADR-0028), so the operator sees what routing is in effect.
func (s *Server) listDispositions(w http.ResponseWriter, r *http.Request) {
	rules, err := s.pdb(r).ListDispositionRules(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type capDispo struct {
		Capability   string                    `json:"capability"`
		Dispositions []disposition.Disposition `json:"dispositions"`
	}
	defaults := make([]capDispo, 0)
	if s.engine != nil {
		for _, m := range s.engine.Registry().Manifests() {
			if len(m.Dispositions) > 0 {
				defaults = append(defaults, capDispo{Capability: m.ID, Dispositions: m.Dispositions})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"overrides": rules, "defaults": defaults})
}

// setDispositionRule adds a project disposition override.
func (s *Server) setDispositionRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CapabilityID string            `json:"capability_id"`
		When         map[string]string `json:"when"`
		MinSeverity  string            `json:"min_severity"`
		Action       string            `json:"action"`
		Priority     int               `json:"priority"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	switch req.Action {
	case disposition.ActionFinding, disposition.ActionInvestigate, disposition.ActionReview:
	default:
		writeErr(w, http.StatusBadRequest, "action must be finding, investigate or review")
		return
	}
	rule, err := s.pdb(r).SetDispositionRule(r.Context(), model.DispositionRule{
		ProjectID: r.PathValue("id"), CapabilityID: req.CapabilityID,
		When: req.When, MinSeverity: req.MinSeverity, Action: req.Action, Priority: req.Priority,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

// deleteDispositionRule removes a project disposition override.
func (s *Server) deleteDispositionRule(w http.ResponseWriter, r *http.Request) {
	if err := s.pdb(r).DeleteDispositionRule(r.Context(), r.PathValue("rule")); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "rule not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
