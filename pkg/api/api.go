// Package api exposes the control-plane HTTP API that every client (desktop, CLI, future web)
// talks to. Domain logic lives in the control-plane packages, never in a client (ADR-0001).
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/version"
)

// Server routes control-plane HTTP requests against the store.
type Server struct {
	mux   *http.ServeMux
	store *store.DB
}

// New builds the API server with its routes registered.
func New(st *store.DB) *Server {
	s := &Server{mux: http.NewServeMux(), store: st}
	s.routes()
	return s
}

// Handler returns the root HTTP handler, wrapped with CORS so a browser-based or Wails frontend
// on another loopback origin can call the API. The API binds to loopback only, so reflecting the
// request origin is safe for a local single-user workbench.
func (s *Server) Handler() http.Handler { return withCORS(s.mux) }

func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /readyz", s.ready)

	s.mux.HandleFunc("GET /v1/organizations", s.listOrganizations)
	s.mux.HandleFunc("POST /v1/organizations", s.createOrganization)

	s.mux.HandleFunc("GET /v1/targets", s.listTargets)
	s.mux.HandleFunc("POST /v1/targets", s.createTarget)

	s.mux.HandleFunc("GET /v1/projects", s.listProjects)
	s.mux.HandleFunc("POST /v1/projects", s.createProject)
	s.mux.HandleFunc("GET /v1/projects/{id}", s.getProject)
	s.mux.HandleFunc("DELETE /v1/projects/{id}", s.deleteProject)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "opensecbench-control-plane",
		"version": version.Version,
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.store.PingContext(r.Context()) != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- organizations ---

func (s *Server) listOrganizations(w http.ResponseWriter, r *http.Request) {
	orgs, err := s.store.ListOrganizations(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, orgs)
}

func (s *Server) createOrganization(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	org, err := s.store.CreateOrganization(r.Context(), req.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

// --- targets ---

func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := s.store.ListTargets(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, targets)
}

func (s *Server) createTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string  `json:"name"`
		Description    string  `json:"description"`
		OrganizationID *string `json:"organization_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	target, err := s.store.CreateTarget(r.Context(), req.Name, req.Description, req.OrganizationID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, target)
}

// --- projects ---

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string   `json:"name"`
		OrganizationID *string  `json:"organization_id"`
		GroupID        *string  `json:"group_id"`
		TargetIDs      []string `json:"target_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	project, err := s.store.CreateProject(r.Context(), store.NewProject{
		Name:           req.Name,
		OrganizationID: req.OrganizationID,
		GroupID:        req.GroupID,
		TargetIDs:      req.TargetIDs,
	})
	if err != nil {
		// TODO(P1+): distinguish constraint violations (e.g. unknown target) as 400.
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteProject(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
