package api

import (
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/policy"
)

func (s *Server) listPolicyProfiles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, policy.All())
}

func (s *Server) getActivePolicy(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.activePolicy())
}

// setActivePolicy switches the active governance profile (audited).
func (s *Server) setActivePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile string `json:"profile"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// Only a known profile is accepted.
	p := policy.Get(req.Profile)
	if p.Name != req.Profile {
		writeErr(w, http.StatusBadRequest, "unknown policy profile "+req.Profile)
		return
	}
	if err := s.store.SetSetting(r.Context(), "active_policy_profile", p.Name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "policy.set", p.Name, nil)
	writeJSON(w, http.StatusOK, p)
}
