// Package api exposes the control-plane HTTP API that every client (desktop, CLI, future web)
// talks to. Domain logic lives in the control-plane packages, never in a client (ADR-0001).
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/analyst"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
	"github.com/opensecbench/opensecbench/pkg/template"
	"github.com/opensecbench/opensecbench/pkg/version"
)

// Deps are the control-plane services the API exposes.
type Deps struct {
	Store    *store.DB
	Engine   *task.Engine
	CAS      *cas.Store
	Provider llm.Provider
}

// Server routes control-plane HTTP requests against the control-plane services.
type Server struct {
	mux      *http.ServeMux
	store    *store.DB
	engine   *task.Engine
	cas      *cas.Store
	provider llm.Provider
}

// New builds the API server with its routes registered.
func New(deps Deps) *Server {
	s := &Server{mux: http.NewServeMux(), store: deps.Store, engine: deps.Engine, cas: deps.CAS, provider: deps.Provider}
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

	s.mux.HandleFunc("GET /v1/search", s.search)

	s.mux.HandleFunc("GET /v1/templates", s.listTemplates)
	s.mux.HandleFunc("POST /v1/projects/from-template", s.createProjectFromTemplate)

	s.mux.HandleFunc("GET /v1/projects", s.listProjects)
	s.mux.HandleFunc("POST /v1/projects", s.createProject)
	s.mux.HandleFunc("GET /v1/projects/{id}", s.getProject)
	s.mux.HandleFunc("DELETE /v1/projects/{id}", s.deleteProject)

	s.mux.HandleFunc("GET /v1/projects/{id}/applications", s.listApplications)
	s.mux.HandleFunc("POST /v1/projects/{id}/applications", s.createApplication)
	s.mux.HandleFunc("GET /v1/applications/{id}", s.getApplication)
	s.mux.HandleFunc("GET /v1/projects/{id}/context", s.listContext)
	s.mux.HandleFunc("POST /v1/projects/{id}/context", s.ingestContext)
	s.mux.HandleFunc("GET /v1/applications/{id}/assets", s.listAssets)
	s.mux.HandleFunc("POST /v1/applications/{id}/assets", s.createAsset)
	s.mux.HandleFunc("GET /v1/assets/{id}", s.getAsset)

	s.mux.HandleFunc("GET /v1/capabilities", s.listCapabilities)
	s.mux.HandleFunc("POST /v1/tasks", s.runTask)
	s.mux.HandleFunc("GET /v1/tasks/{id}", s.getTask)
	s.mux.HandleFunc("GET /v1/tasks/{id}/artifacts", s.getTaskArtifacts)
	s.mux.HandleFunc("GET /v1/artifacts/{id}/content", s.getArtifactContent)

	s.mux.HandleFunc("GET /v1/tasks/{id}/observations", s.getTaskObservations)
	s.mux.HandleFunc("POST /v1/observations/{id}/review", s.reviewObservation)

	s.mux.HandleFunc("GET /v1/findings", s.listFindings)
	s.mux.HandleFunc("POST /v1/findings", s.createFinding)
	s.mux.HandleFunc("GET /v1/findings/{id}", s.getFinding)

	s.mux.HandleFunc("POST /v1/analyst/ask", s.analystAsk)
}

func (s *Server) analystAsk(w http.ResponseWriter, r *http.Request) {
	if s.provider == nil {
		writeErr(w, http.StatusServiceUnavailable, "no LLM provider configured (set OSB_LLM_PROVIDER)")
		return
	}
	var req struct {
		Message string   `json:"message"`
		Allow   []string `json:"allow"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Message == "" {
		writeErr(w, http.StatusBadRequest, "message is required")
		return
	}
	// TODO(P4): async approval queue, thread persistence, budgets, and data-egress policy.
	// req.Allow authorizes gated tools (e.g. run_capability) for this ask only.
	loop := analyst.NewLoop(s.provider, s.store, s.engine, req.Allow, nil)
	res, err := loop.Run(r.Context(), req.Message)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
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

// --- search ---

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	results, err := s.store.Search(r.Context(), r.URL.Query().Get("q"), 25)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// --- templates ---

func (s *Server) listTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, template.BuiltIns())
}

func (s *Server) createProjectFromTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TemplateID string `json:"template_id"`
		Name       string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	tmpl, ok := template.Get(req.TemplateID)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown template "+req.TemplateID)
		return
	}

	proj, err := s.store.CreateProject(r.Context(), store.NewProject{Name: req.Name})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := map[string]any{"project": proj, "template": tmpl}
	if tmpl.DefaultApplication != "" {
		app, err := s.store.CreateApplication(r.Context(), proj.ID, tmpl.DefaultApplication)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp["application"] = app
	}
	writeJSON(w, http.StatusCreated, resp)
}

// --- applications & assets ---

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.ListApplicationsByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) {
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
	app, err := s.store.CreateApplication(r.Context(), r.PathValue("id"), req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GetApplication(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "application not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, app)
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	assets, err := s.store.ListAssetsByApplication(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, assets)
}

func (s *Server) createAsset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type        string `json:"type"`
		Location    string `json:"location"`
		Sensitivity string `json:"sensitivity"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	asset, err := s.store.CreateAsset(r.Context(), store.NewAsset{
		ApplicationID: r.PathValue("id"),
		Type:          req.Type,
		Location:      req.Location,
		Sensitivity:   req.Sensitivity,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, asset)
}

func (s *Server) getAsset(w http.ResponseWriter, r *http.Request) {
	asset, err := s.store.GetAsset(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

// --- context items ---

func (s *Server) listContext(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListContextItemsByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// ingestContext stores a request body in the CAS as an input artifact and records a context item.
// Metadata comes from query params (name, type); content type is taken from the header.
func (s *Server) ingestContext(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name query parameter is required")
		return
	}
	ctype := r.URL.Query().Get("type")
	if ctype == "" {
		ctype = model.ContextDocument
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<20) // 64 MiB cap
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	digest, err := s.cas.Put(bytes.NewReader(data))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	mediaType := r.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	art, err := s.store.CreateArtifact(r.Context(), model.Artifact{
		SHA256:    digest,
		Size:      int64(len(data)),
		Kind:      model.ArtifactInput,
		Name:      name,
		MediaType: mediaType,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ci, err := s.store.CreateContextItem(r.Context(), model.ContextItem{
		ProjectID:  r.PathValue("id"),
		Type:       ctype,
		Name:       name,
		ArtifactID: art.ID,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ci)
}

// --- capabilities, tasks, artifacts ---

func (s *Server) listCapabilities(w http.ResponseWriter, _ *http.Request) {
	if s.engine == nil {
		writeErr(w, http.StatusServiceUnavailable, "engine unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.engine.Registry().Manifests())
}

func (s *Server) runTask(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeErr(w, http.StatusServiceUnavailable, "engine unavailable")
		return
	}
	var req struct {
		CapabilityID  string         `json:"capability_id"`
		TargetDir     string         `json:"target_dir"`
		Actor         string         `json:"actor"`
		AssetID       *string        `json:"asset_id"`
		ApplicationID *string        `json:"application_id"`
		Params        map[string]any `json:"params"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.CapabilityID == "" {
		writeErr(w, http.StatusBadRequest, "capability_id is required")
		return
	}
	out, err := s.engine.Run(r.Context(), task.RunRequest{
		CapabilityID:  req.CapabilityID,
		TargetDir:     req.TargetDir,
		Actor:         req.Actor,
		AssetID:       req.AssetID,
		ApplicationID: req.ApplicationID,
		Params:        req.Params,
	})
	// A validation/plan error produces no task; a run failure produces a failed task the caller
	// still wants to inspect.
	if err != nil && out.Task.ID == "" {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTask(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) getTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	arts, err := s.store.ListArtifactsByTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, arts)
}

func (s *Server) getArtifactContent(w http.ResponseWriter, r *http.Request) {
	art, err := s.store.GetArtifact(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "artifact not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rc, err := s.cas.Open(art.SHA256)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "artifact bytes unavailable")
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", art.MediaType)
	_, _ = io.Copy(w, rc)
}

// --- observations & findings ---

func (s *Server) getTaskObservations(w http.ResponseWriter, r *http.Request) {
	obs, err := s.store.ListObservationsByTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, obs)
}

func (s *Server) reviewObservation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.store.ReviewObservation(r.Context(), r.PathValue("id"), req.State)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "observation not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listFindings(w http.ResponseWriter, r *http.Request) {
	findings, err := s.store.ListFindings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, findings)
}

func (s *Server) createFinding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ApplicationID  *string  `json:"application_id"`
		Title          string   `json:"title"`
		Severity       string   `json:"severity"`
		Description    string   `json:"description"`
		CWE            string   `json:"cwe"`
		ObservationIDs []string `json:"observation_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, "title is required")
		return
	}
	f, err := s.store.CreateFinding(r.Context(), store.NewFinding{
		ApplicationID:  req.ApplicationID,
		Title:          req.Title,
		Severity:       req.Severity,
		Description:    req.Description,
		CWE:            req.CWE,
		ObservationIDs: req.ObservationIDs,
	})
	if err != nil {
		// Rejected for unconfirmed/unknown observations, etc.
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) getFinding(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.GetFinding(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "finding not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, f)
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
