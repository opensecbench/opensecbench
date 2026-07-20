package api

import (
	"errors"
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// listProjectKB returns the KB a project inherits from the targets it references (ADR-0010).
func (s *Server) listProjectKB(w http.ResponseWriter, r *http.Request) {
	entries, err := s.mgr.ListKBForProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) listTargetKB(w http.ResponseWriter, r *http.Request) {
	entries, err := s.global().ListKBByTarget(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) createKBEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind        string `json:"kind"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		Tags        string `json:"tags"`
		Sensitivity string `json:"sensitivity"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	entry, err := s.global().CreateKBEntry(r.Context(), model.KBEntry{
		TargetID:    r.PathValue("id"),
		Kind:        req.Kind,
		Title:       req.Title,
		Body:        req.Body,
		Tags:        req.Tags,
		Sensitivity: req.Sensitivity,
		Origin:      model.OriginHuman,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "kb.create", entry.ID, map[string]string{"target": entry.TargetID, "kind": entry.Kind})
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) getKBEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := s.global().GetKBEntry(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "kb entry not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) updateKBEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Tags  string `json:"tags"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if err := s.global().UpdateKBEntry(r.Context(), id, req.Title, req.Body, req.Tags); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "kb entry not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	entry, _ := s.global().GetKBEntry(r.Context(), id)
	s.record(r.Context(), actorOf(r), "kb.update", id, nil)
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) reviewKBEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if err := s.global().ReviewKBEntry(r.Context(), id, req.State); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "kb entry not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "kb.review", id, map[string]string{"state": req.State})
	entry, _ := s.global().GetKBEntry(r.Context(), id)
	writeJSON(w, http.StatusOK, entry)
}

// verifyKBEntry bumps a fact's freshness — "still true as of now" — without changing its review state (ADR-0043).
func (s *Server) verifyKBEntry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.global().VerifyKBEntry(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "kb entry not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "kb.verify", id, nil)
	entry, _ := s.global().GetKBEntry(r.Context(), id)
	writeJSON(w, http.StatusOK, entry)
}
