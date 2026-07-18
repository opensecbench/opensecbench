package api

import (
	"net/http"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/methodology"
)

// listMethodologies returns the full methodology catalog (all packs + items).
func (s *Server) listMethodologies(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.methods.All())
}

// getMethodologyCoverage returns a project's adopted packs with per-item status and a roll-up.
func (s *Server) getMethodologyCoverage(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	adopted, err := s.store.ListAdoptedMethodologies(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries, err := s.store.ListCoverage(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	states := make(map[string]methodology.State, len(entries))
	for _, e := range entries {
		states[e.ItemID] = methodology.State{Status: e.Status, Note: e.Note}
	}
	writeJSON(w, http.StatusOK, methodology.BuildCoverage(s.methods, adopted, states))
}

// methodologySuggestions recommends packs to adopt based on the project's inherited knowledge base.
func (s *Server) methodologySuggestions(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	kb, err := s.store.ListKBByProject(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var sb strings.Builder
	for _, e := range kb {
		sb.WriteString(e.Kind)
		sb.WriteByte(' ')
		sb.WriteString(e.Title)
		sb.WriteByte(' ')
		sb.WriteString(e.Body)
		sb.WriteByte(' ')
		sb.WriteString(e.Tags)
		sb.WriteByte('\n')
	}
	adopted, _ := s.store.ListAdoptedMethodologies(r.Context(), projectID)
	writeJSON(w, http.StatusOK, methodology.Suggest(s.methods, sb.String(), adopted))
}

func (s *Server) adoptMethodology(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var req struct {
		MethodologyID string `json:"methodology_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, ok := s.methods.Get(req.MethodologyID); !ok {
		writeErr(w, http.StatusBadRequest, "unknown methodology "+req.MethodologyID)
		return
	}
	if err := s.store.AdoptMethodology(r.Context(), projectID, req.MethodologyID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "methodology.adopt", projectID, map[string]string{"methodology": req.MethodologyID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unadoptMethodology(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var req struct {
		MethodologyID string `json:"methodology_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.UnadoptMethodology(r.Context(), projectID, req.MethodologyID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setCoverage records the operator's status + note for a methodology item.
func (s *Server) setCoverage(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var req struct {
		ItemID string `json:"item_id"`
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, _, ok := s.methods.Item(req.ItemID); !ok {
		writeErr(w, http.StatusBadRequest, "unknown methodology item "+req.ItemID)
		return
	}
	if err := s.store.SetCoverage(r.Context(), projectID, req.ItemID, req.Status, req.Note); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "coverage.set", req.ItemID, map[string]string{"project": projectID, "status": req.Status})
	w.WriteHeader(http.StatusNoContent)
}
