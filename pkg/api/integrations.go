package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/integration"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// listIntegrations returns the available connector TYPES (jira/defectdojo) — the vocabulary for creating a
// connector in the Library.
func (s *Server) listIntegrations(w http.ResponseWriter, _ *http.Request) {
	type typeView struct {
		Type     string `json:"type"`
		Pullable bool   `json:"pullable"`
	}
	out := make([]typeView, 0)
	for _, name := range s.integr.Names() {
		c, _ := s.integr.Get(name)
		_, pullable := c.(integration.Puller)
		out = append(out, typeView{Type: name, Pullable: pullable})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- Global connectors (Library) ---

func (s *Server) listConnectors(w http.ResponseWriter, r *http.Request) {
	cs, err := s.global().ListConnectors(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cs == nil {
		cs = []model.Connector{}
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) createConnector(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		BaseURL    string `json:"base_url"`
		Credential string `json:"credential"` // vault secret NAME
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, ok := s.integr.Get(req.Type); !ok {
		writeErr(w, http.StatusBadRequest, "unknown connector type "+req.Type)
		return
	}
	c, err := s.global().CreateConnector(r.Context(), model.Connector{
		Name: req.Name, Type: req.Type, BaseURL: req.BaseURL, Credential: req.Credential,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "connector.add", c.ID, map[string]string{"type": c.Type})
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) deleteConnector(w http.ResponseWriter, r *http.Request) {
	if err := s.global().DeleteConnector(r.Context(), r.PathValue("id")); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "connector not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Per-project bindings ---

// listProjectIntegrations returns each global connector merged with this project's binding state — bound?
// its project_key? does the type support inbound pull?
func (s *Server) listProjectIntegrations(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	connectors, err := s.global().ListConnectors(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	bindings, err := s.pdb(r).ListBindings(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	bound := map[string]model.IntegrationBinding{}
	for _, b := range bindings {
		bound[b.ConnectorID] = b
	}
	type view struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Type       string `json:"type"`
		BaseURL    string `json:"base_url"`
		Pullable   bool   `json:"pullable"`
		Bound      bool   `json:"bound"`
		ProjectKey string `json:"project_key"`
	}
	out := make([]view, 0, len(connectors))
	for _, c := range connectors {
		pullable := false
		if impl, ok := s.integr.Get(c.Type); ok {
			_, pullable = impl.(integration.Puller)
		}
		v := view{ID: c.ID, Name: c.Name, Type: c.Type, BaseURL: c.BaseURL, Pullable: pullable}
		if b, ok := bound[c.ID]; ok {
			v.Bound, v.ProjectKey = true, b.ProjectKey
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"connectors": out})
}

// setBinding attaches (or updates) this project's binding to a connector with a project-side scope.
func (s *Server) setBinding(w http.ResponseWriter, r *http.Request) {
	projectID, connectorID := r.PathValue("id"), r.PathValue("connectorId")
	if _, err := s.global().GetConnector(r.Context(), connectorID); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "unknown connector")
		return
	}
	var req struct {
		ProjectKey string `json:"project_key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := s.pdb(r).SetBinding(r.Context(), model.IntegrationBinding{
		ProjectID: projectID, ConnectorID: connectorID, ProjectKey: req.ProjectKey,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "integration.bind", connectorID, map[string]string{"project": projectID})
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) deleteBinding(w http.ResponseWriter, r *http.Request) {
	if err := s.pdb(r).DeleteBinding(r.Context(), r.PathValue("id"), r.PathValue("connectorId")); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such binding")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listFindingLinks(w http.ResponseWriter, r *http.Request) {
	links, err := s.pdb(r).ListExternalLinks(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, links)
}

// configForConnector assembles the runtime push/pull config from the global connector (base URL +
// resolved credential) and this project's binding (project-side scope). It also returns the connector's
// type so the caller can select the connector implementation.
func (s *Server) configForConnector(ctx context.Context, projectID, connectorID string) (integration.Config, string, error) {
	c, err := s.global().GetConnector(ctx, connectorID)
	if err != nil {
		return integration.Config{}, "", err
	}
	cred, err := s.resolveCredential(ctx, c.Credential)
	if err != nil {
		return integration.Config{}, "", err
	}
	cfg := integration.Config{BaseURL: c.BaseURL, Credential: cred}
	if b, err := s.pdbID(projectID).GetBinding(ctx, projectID, connectorID); err == nil {
		cfg.ProjectKey = b.ProjectKey
	}
	return cfg, c.Type, nil
}

// pushFinding sends a finding to an external tracker via a connector, recording an idempotent external
// link (re-push returns the existing link).
func (s *Server) pushFinding(w http.ResponseWriter, r *http.Request) {
	findingID := r.PathValue("id")
	var req struct {
		ConnectorID string `json:"connector_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	cfg, connType, err := s.configForConnector(r.Context(), s.pdbProjectID(r), req.ConnectorID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "unknown connector")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	conn, ok := s.integr.Get(connType)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown connector type "+connType)
		return
	}
	// Idempotency: keyed by connector id so the same finding isn't double-pushed to the same tracker.
	if existing, err := s.pdb(r).GetExternalLink(r.Context(), findingID, req.ConnectorID); err == nil {
		writeJSON(w, http.StatusOK, existing)
		return
	}
	finding, err := s.pdb(r).GetFinding(r.Context(), findingID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "finding not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ref, err := conn.PushFinding(r.Context(), cfg, finding)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "push: "+err.Error())
		return
	}
	link, err := s.pdb(r).CreateExternalLink(r.Context(), model.ExternalLink{
		FindingID: findingID, Integration: req.ConnectorID, ExternalID: ref.ID, ExternalURL: ref.URL,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "integration.push", findingID, map[string]string{
		"connector": req.ConnectorID, "external_id": ref.ID,
	})
	writeJSON(w, http.StatusCreated, link)
}

// resolveCredential opens a vault secret by name (never a value). Empty name → empty credential.
func (s *Server) resolveCredential(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if s.vault == nil {
		return "", errors.New("vault unavailable for credential resolution")
	}
	sealed, err := s.global().GetSealed(ctx, name)
	if err != nil {
		return "", errors.New("unknown credential secret " + name)
	}
	v, err := s.vault.Open(sealed)
	if err != nil {
		return "", errors.New("open credential: " + err.Error())
	}
	return string(v), nil
}

// pdbProjectID resolves the active project id for a request (X-Project-Id header / query / path).
func (s *Server) pdbProjectID(r *http.Request) string { return projectFromReq(r) }

// pullIntegration imports external findings via a connector into the project as unreviewed observations
// (ADR-0027), deduped by external id so a re-pull only brings in new ones.
func (s *Server) pullIntegration(w http.ResponseWriter, r *http.Request) {
	projectID, connectorID := r.PathValue("id"), r.PathValue("connectorId")
	cfg, connType, err := s.configForConnector(r.Context(), projectID, connectorID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "unknown connector")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	conn, ok := s.integr.Get(connType)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown connector type "+connType)
		return
	}
	puller, ok := conn.(integration.Puller)
	if !ok {
		writeErr(w, http.StatusBadRequest, connType+" does not support pull")
		return
	}
	ext, err := puller.Pull(r.Context(), cfg)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "pull: "+err.Error())
		return
	}
	imported, skipped := 0, 0
	for _, ef := range ext {
		if ef.ExternalID == "" {
			continue
		}
		if has, _ := s.pdb(r).HasImport(r.Context(), projectID, connectorID, ef.ExternalID); has {
			skipped++
			continue
		}
		review := model.ReviewUnreviewed
		if ef.Confirmed {
			review = model.ReviewConfirmed
		}
		pid := projectID
		obs, err := s.pdb(r).CreateObservation(r.Context(), model.Observation{
			ProjectID: &pid, Origin: model.OriginTool, ReviewState: review,
			Title: ef.Title, Detail: ef.Detail, Severity: normalizeSeverity(ef.Severity),
			RuleID: connType + ":" + ef.ExternalID, Location: ef.URL,
		})
		if err != nil {
			continue
		}
		_ = s.pdb(r).RecordImport(r.Context(), projectID, connectorID, ef.ExternalID, obs.ID)
		imported++
	}
	s.record(r.Context(), actorOf(r), "integration.pull", projectID, map[string]any{
		"connector": connectorID, "imported": imported, "skipped": skipped,
	})
	writeJSON(w, http.StatusOK, map[string]int{"imported": imported, "skipped": skipped, "total": len(ext)})
}

// normalizeSeverity maps an external severity onto OSB's scale (the observation CHECK constraint).
func normalizeSeverity(s string) string {
	switch s {
	case "critical", "high", "medium", "low", "info":
		return s
	case "informational":
		return "info"
	default:
		return "info"
	}
}
