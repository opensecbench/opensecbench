package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/opensecbench/opensecbench/pkg/store"
)

func (s *Server) listCanaries(w http.ResponseWriter, r *http.Request) {
	items, err := s.global().ListCanaries(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// createCanary plants a new decoy token. The token is returned so the operator can place it.
func (s *Server) createCanary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label string `json:"label"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := s.global().CreateCanary(r.Context(), req.Label)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "canary.create", c.ID, map[string]string{"label": c.Label})
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) deleteCanary(w http.ResponseWriter, r *http.Request) {
	if err := s.global().DeleteCanary(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "canary not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listDLPEvents(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	events, err := s.global().ListDLPEvents(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}
