package api

import (
	"net/http"
	"time"

	"github.com/opensecbench/opensecbench/pkg/dossier"
)

// targetDossier assembles a target's inherited knowledge into a consolidated dossier (ADR-0042).
// `?format=markdown` returns the rendered brief; otherwise JSON.
func (s *Server) targetDossier(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	entries, err := s.global().ListKBByTarget(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	subject := id
	if t, err := s.global().GetTarget(r.Context(), id); err == nil && t.Name != "" {
		subject = t.Name
	}
	s.writeDossier(w, r, dossier.Assemble(subject, entries, time.Now()))
}

// projectDossier assembles a project's inherited knowledge (its target(s) + group + org + global).
func (s *Server) projectDossier(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	entries, err := s.mgr.ListKBForProject(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	subject := id
	if p, err := s.mgr.GetProject(r.Context(), id); err == nil && p.Name != "" {
		subject = p.Name
	}
	s.writeDossier(w, r, dossier.Assemble(subject, entries, time.Now()))
}

func (s *Server) writeDossier(w http.ResponseWriter, r *http.Request, d dossier.Dossier) {
	if r.URL.Query().Get("format") == "markdown" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(d.Markdown()))
		return
	}
	writeJSON(w, http.StatusOK, d)
}
