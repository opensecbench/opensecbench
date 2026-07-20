package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/integration"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func (s *Server) listIntegrations(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.integr.Names())
}

func (s *Server) listFindingLinks(w http.ResponseWriter, r *http.Request) {
	links, err := s.pdb(r).ListExternalLinks(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, links)
}

// pushFinding sends a finding to an external tracker, resolving its credential from the vault and
// recording an idempotent external link (re-push returns the existing link) (P10).
func (s *Server) pushFinding(w http.ResponseWriter, r *http.Request) {
	findingID := r.PathValue("id")
	var req struct {
		Integration string `json:"integration"`
		BaseURL     string `json:"base_url"`
		ProjectKey  string `json:"project_key"`
		Credential  string `json:"credential"` // a vault secret NAME (not a value)
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	conn, ok := s.integr.Get(req.Integration)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown integration "+req.Integration)
		return
	}

	// Idempotency: if already linked, return the existing link.
	if existing, err := s.pdb(r).GetExternalLink(r.Context(), findingID, req.Integration); err == nil {
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

	// Resolve the connection: an explicit base_url in the body overrides (legacy path); otherwise use the
	// project's stored config (ADR-0027), so connection details aren't re-sent on every push.
	var cfg integration.Config
	if req.BaseURL != "" {
		cred, cerr := s.resolveCredential(r.Context(), req.Credential)
		if cerr != nil {
			writeErr(w, http.StatusBadRequest, cerr.Error())
			return
		}
		cfg = integration.Config{BaseURL: req.BaseURL, ProjectKey: req.ProjectKey, Credential: cred}
	} else {
		projectID := s.projectOfFinding(r.Context(), finding)
		if projectID == "" {
			writeErr(w, http.StatusBadRequest, "no base_url and the finding has no project to resolve a stored integration config")
			return
		}
		cfg, err = s.integrationConfig(r.Context(), projectID, req.Integration)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "no stored config for "+req.Integration+"; configure it (or pass base_url)")
			return
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	ref, err := conn.PushFinding(r.Context(), cfg, finding)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "push: "+err.Error())
		return
	}

	link, err := s.pdb(r).CreateExternalLink(r.Context(), model.ExternalLink{
		FindingID: findingID, Integration: req.Integration, ExternalID: ref.ID, ExternalURL: ref.URL,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "integration.push", findingID, map[string]string{
		"integration": req.Integration, "external_id": ref.ID,
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

// integrationConfig loads a project's stored integration config and resolves its credential (ADR-0027).
func (s *Server) integrationConfig(ctx context.Context, projectID, name string) (integration.Config, error) {
	c, err := s.pdbID(projectID).GetIntegrationConfig(ctx, projectID, name)
	if err != nil {
		return integration.Config{}, err
	}
	cred, err := s.resolveCredential(ctx, c.Credential)
	if err != nil {
		return integration.Config{}, err
	}
	return integration.Config{BaseURL: c.BaseURL, ProjectKey: c.ProjectKey, Credential: cred}, nil
}

// projectOfFinding resolves a finding's project via its application (findings scope through applications).
func (s *Server) projectOfFinding(ctx context.Context, f model.Finding) string {
	if f.ApplicationID == nil {
		return ""
	}
	app, err := s.global().GetApplication(ctx, *f.ApplicationID)
	if err != nil {
		return ""
	}
	return app.ProjectID
}

// listProjectIntegrations returns a project's configured integrations plus the available connectors and
// whether each supports inbound pull.
func (s *Server) listProjectIntegrations(w http.ResponseWriter, r *http.Request) {
	configs, err := s.pdb(r).ListIntegrationConfigs(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type connView struct {
		Name     string `json:"name"`
		Pullable bool   `json:"pullable"`
	}
	conns := make([]connView, 0)
	for _, name := range s.integr.Names() {
		c, _ := s.integr.Get(name)
		_, pullable := c.(integration.Puller)
		conns = append(conns, connView{Name: name, Pullable: pullable})
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": configs, "connectors": conns})
}

// setIntegrationConfig upserts a project's config for an integration (credential is a vault secret name).
func (s *Server) setIntegrationConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("integration")
	if _, ok := s.integr.Get(name); !ok {
		writeErr(w, http.StatusBadRequest, "unknown integration "+name)
		return
	}
	var req struct {
		BaseURL    string `json:"base_url"`
		ProjectKey string `json:"project_key"`
		Credential string `json:"credential"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	cfg, err := s.pdb(r).SetIntegrationConfig(r.Context(), model.IntegrationConfig{
		ProjectID: r.PathValue("id"), Integration: name,
		BaseURL: req.BaseURL, ProjectKey: req.ProjectKey, Credential: req.Credential,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "integration.config", name, map[string]string{"project": r.PathValue("id")})
	writeJSON(w, http.StatusOK, cfg)
}

// deleteIntegrationConfig removes a project's config for an integration.
func (s *Server) deleteIntegrationConfig(w http.ResponseWriter, r *http.Request) {
	if err := s.pdb(r).DeleteIntegrationConfig(r.Context(), r.PathValue("id"), r.PathValue("integration")); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such integration config")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pullIntegration imports external findings into the project as unreviewed observations (ADR-0027),
// deduped by external id so a re-pull only brings in new ones.
func (s *Server) pullIntegration(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	name := r.PathValue("integration")
	conn, ok := s.integr.Get(name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown integration "+name)
		return
	}
	puller, ok := conn.(integration.Puller)
	if !ok {
		writeErr(w, http.StatusBadRequest, name+" does not support pull")
		return
	}
	cfg, err := s.integrationConfig(r.Context(), projectID, name)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "no config for "+name+"; configure it first")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
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
		if has, _ := s.pdb(r).HasImport(r.Context(), projectID, name, ef.ExternalID); has {
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
			RuleID: name + ":" + ef.ExternalID, Location: ef.URL,
		})
		if err != nil {
			continue
		}
		_ = s.pdb(r).RecordImport(r.Context(), projectID, name, ef.ExternalID, obs.ID)
		imported++
	}
	s.record(r.Context(), actorOf(r), "integration.pull", projectID, map[string]any{
		"integration": name, "imported": imported, "skipped": skipped,
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
