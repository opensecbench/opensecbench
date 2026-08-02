package api

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// Classification levels are the user-configurable data-classification scale (governance): one ordered set
// of tiers shared by asset sensitivity and destination clearance. Built-ins (open_source/internal/private)
// are permanent — renamable/reorderable but never deleted.

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// slugify derives a stable id from a label (lowercase, non-alphanumerics → underscore).
func slugify(s string) string {
	return strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "_"), "_")
}

func (s *Server) listClassificationLevels(w http.ResponseWriter, r *http.Request) {
	levels, err := s.global().ListClassificationLevels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if levels == nil {
		levels = []model.ClassificationLevel{}
	}
	writeJSON(w, http.StatusOK, levels)
}

func (s *Server) createClassificationLevel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Rank  int    `json:"rank"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		writeErr(w, http.StatusBadRequest, "label required")
		return
	}
	id := req.ID
	if id == "" {
		id = slugify(req.Label)
	}
	if id == "" {
		writeErr(w, http.StatusBadRequest, "could not derive an id from the label")
		return
	}
	if s.global().LoadScale(r.Context()).Has(id) {
		writeErr(w, http.StatusConflict, "a classification level with id "+id+" already exists")
		return
	}
	saved, err := s.global().CreateClassificationLevel(r.Context(), model.ClassificationLevel{ID: id, Label: req.Label, Rank: req.Rank, Color: req.Color})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "classification.add", saved.ID, map[string]string{"label": saved.Label})
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) updateClassificationLevel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Label string `json:"label"`
		Rank  int    `json:"rank"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		writeErr(w, http.StatusBadRequest, "label required")
		return
	}
	if err := s.global().UpdateClassificationLevel(r.Context(), model.ClassificationLevel{ID: id, Label: req.Label, Rank: req.Rank, Color: req.Color}); err != nil {
		writeErr(w, http.StatusNotFound, "classification level not found")
		return
	}
	s.record(r.Context(), actorOf(r), "classification.update", id, map[string]string{"label": req.Label})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteClassificationLevel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.global().DeleteClassificationLevel(r.Context(), id); err != nil {
		// Built-in / in-use refusals and not-found all surface as a clear client error.
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "classification.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}
