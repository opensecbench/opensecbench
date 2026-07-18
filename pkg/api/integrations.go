package api

import (
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
	links, err := s.store.ListExternalLinks(r.Context(), r.PathValue("id"))
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
	if existing, err := s.store.GetExternalLink(r.Context(), findingID, req.Integration); err == nil {
		writeJSON(w, http.StatusOK, existing)
		return
	}

	finding, err := s.store.GetFinding(r.Context(), findingID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "finding not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Resolve the credential from the vault (a reference by name, never a raw value).
	cred := ""
	if req.Credential != "" {
		if s.vault == nil {
			writeErr(w, http.StatusServiceUnavailable, "vault unavailable for credential resolution")
			return
		}
		sealed, err := s.store.GetSealed(r.Context(), req.Credential)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "unknown credential secret "+req.Credential)
			return
		}
		v, err := s.vault.Open(sealed)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "open credential: "+err.Error())
			return
		}
		cred = string(v)
	}

	ref, err := conn.PushFinding(r.Context(), integration.Config{
		BaseURL: req.BaseURL, ProjectKey: req.ProjectKey, Credential: cred,
	}, finding)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "push: "+err.Error())
		return
	}

	link, err := s.store.CreateExternalLink(r.Context(), model.ExternalLink{
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
