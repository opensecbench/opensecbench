package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// envProviderInfo describes the provider built from OSB_LLM_* at startup (the dev bootstrap).
func envProviderInfo(built llm.Provider) providerInfo {
	t := os.Getenv("OSB_LLM_PROVIDER")
	if t == "" {
		t = "mock"
	}
	return providerInfo{
		Name: "environment", Type: t, Model: os.Getenv("OSB_LLM_MODEL"),
		IsLocal:    built != nil && llm.IsLocal(built),
		Configured: t != "mock",
	}
}

const activeProviderSetting = "analyst.active_provider"

// providerInfo is the active-provider view shown in the Analyst UI (never a key).
type providerInfo struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Model      string `json:"model"`
	IsLocal    bool   `json:"is_local"`
	Configured bool   `json:"configured"` // false for mock / unconfigured
}

func infoFor(id string, p model.Provider, built llm.Provider) providerInfo {
	return providerInfo{
		ID: id, Name: p.Name, Type: p.Type, Model: p.Model,
		IsLocal:    built != nil && llm.IsLocal(built),
		Configured: p.Type != "" && p.Type != "mock",
	}
}

// llmProvider returns the active raw provider (DLP-wrapped by guardedProvider on use).
func (s *Server) llmProvider() llm.Provider {
	s.providerMu.RLock()
	defer s.providerMu.RUnlock()
	return s.provider
}

// activeInfo returns the display info for the active provider.
func (s *Server) activeInfo() providerInfo {
	s.providerMu.RLock()
	defer s.providerMu.RUnlock()
	return s.activeProvider
}

func (s *Server) setProvider(built llm.Provider, info providerInfo) {
	s.providerMu.Lock()
	s.provider = built
	s.activeProvider = info
	s.providerMu.Unlock()
}

// buildProvider constructs a live provider from a stored registration, resolving its vault-sealed key.
func (s *Server) buildProvider(p model.Provider) (llm.Provider, error) {
	key := ""
	if p.KeySealed != "" {
		if s.vault == nil {
			return nil, errors.New("vault unavailable")
		}
		b, err := s.vault.Open(p.KeySealed)
		if err != nil {
			return nil, fmt.Errorf("open credential: %w", err)
		}
		key = string(b)
	}
	return llm.New(llm.Config{Type: p.Type, BaseURL: p.BaseURL, Model: p.Model, APIKey: key, NativeTools: os.Getenv("OSB_LLM_NATIVE_TOOLS") != "0"})
}

// loadActiveProvider swaps in the persisted active provider on startup (falling back to the env
// provider if none is set or it fails to build).
func (s *Server) loadActiveProvider() {
	if s.mgr == nil {
		return
	}
	id, err := s.global().GetSetting(context.Background(), activeProviderSetting)
	if err != nil || id == "" {
		return
	}
	p, err := s.global().GetProvider(context.Background(), id)
	if err != nil {
		return
	}
	built, err := s.buildProvider(p)
	if err != nil {
		log.Printf("analyst: active provider %q failed to load: %v", p.Name, err)
		return
	}
	s.setProvider(built, infoFor(id, p, built))
}

// --- handlers ---

// getActiveProvider reports the model the interactive chat runs on: the "default" tag's top entry
// (ADR-0052), falling back to the bootstrap active provider when no default routing is set.
func (s *Server) getActiveProvider(w http.ResponseWriter, r *http.Request) {
	if ref, ok := s.analystService().DefaultRoutingRef(r.Context()); ok {
		if p, err := s.global().GetProvider(r.Context(), ref.ProviderID); err == nil {
			built, _ := s.buildProvider(p)
			info := infoFor(p.ID, p, built)
			if ref.Model != "" {
				info.Model = ref.Model
			}
			writeJSON(w, http.StatusOK, info)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.activeInfo())
}

type providerView struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Model         string    `json:"model"`
	BaseURL       string    `json:"base_url"`
	HasKey        bool      `json:"has_key"`
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"created_at"`
	DataClearance string    `json:"data_clearance"`
	ClearanceNote string    `json:"clearance_note"`
}

func viewOfProvider(p model.Provider, activeID string) providerView {
	return providerView{
		ID: p.ID, Name: p.Name, Type: p.Type, Model: p.Model, BaseURL: p.BaseURL,
		HasKey: p.KeySealed != "", Active: p.ID == activeID, CreatedAt: p.CreatedAt,
		DataClearance: p.DataClearance, ClearanceNote: p.ClearanceNote,
	}
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := s.global().ListProviders(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	activeID := s.activeInfo().ID
	views := make([]providerView, 0, len(ps))
	for _, p := range ps {
		views = append(views, viewOfProvider(p, activeID))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) addProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		Type          string `json:"type"`
		Model         string `json:"model"`
		BaseURL       string `json:"base_url"`
		APIKey        string `json:"api_key"`
		DataClearance string `json:"data_clearance"`
		ClearanceNote string `json:"clearance_note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DataClearance != "" && !validClearance(req.DataClearance) {
		writeErr(w, http.StatusBadRequest, "invalid data_clearance "+req.DataClearance)
		return
	}
	sealed := ""
	if req.APIKey != "" {
		if s.vault == nil {
			writeErr(w, http.StatusServiceUnavailable, "vault unavailable — cannot store a key")
			return
		}
		b, err := s.vault.Seal([]byte(req.APIKey))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "seal: "+err.Error())
			return
		}
		sealed = b
	}
	p := model.Provider{Name: req.Name, Type: req.Type, Model: req.Model, BaseURL: req.BaseURL, KeySealed: sealed, DataClearance: req.DataClearance, ClearanceNote: req.ClearanceNote}
	// Validate the configuration builds (catches unknown type / missing base URL) before persisting.
	if _, err := s.buildProvider(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.global().CreateProvider(r.Context(), p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "provider.add", saved.ID, map[string]string{"type": saved.Type, "model": saved.Model, "clearance": saved.DataClearance})
	writeJSON(w, http.StatusCreated, viewOfProvider(saved, s.activeInfo().ID))
}

// validClearance reports whether a string is one of the asset-sensitivity tiers usable as a clearance.
func validClearance(c string) bool {
	return c == model.SensitivityOpenSource || c == model.SensitivityInternal || c == model.SensitivityPrivate
}

// setProviderClearance approves a connection for a data-clearance tier (audited). The note records why —
// e.g. the governing DPA.
func (s *Server) setProviderClearance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		DataClearance string `json:"data_clearance"`
		ClearanceNote string `json:"clearance_note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validClearance(req.DataClearance) {
		writeErr(w, http.StatusBadRequest, "invalid data_clearance "+req.DataClearance)
		return
	}
	if err := s.global().SetProviderClearance(r.Context(), id, req.DataClearance, req.ClearanceNote); err != nil {
		writeErr(w, http.StatusNotFound, "provider not found")
		return
	}
	s.record(r.Context(), actorOf(r), "provider.clearance", id, map[string]string{"clearance": req.DataClearance})
	p, err := s.global().GetProvider(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewOfProvider(p, s.activeInfo().ID))
}

// setConnectionModelClearance overrides a single model's clearance below its connection's (or clears the
// override with an empty tier so it inherits again). Audited.
func (s *Server) setConnectionModelClearance(w http.ResponseWriter, r *http.Request) {
	connID := r.PathValue("id")
	var req struct {
		ModelID       string `json:"model_id"`
		DataClearance string `json:"data_clearance"`
		ClearanceNote string `json:"clearance_note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ModelID == "" {
		writeErr(w, http.StatusBadRequest, "model_id required")
		return
	}
	if req.DataClearance != "" && !validClearance(req.DataClearance) {
		writeErr(w, http.StatusBadRequest, "invalid data_clearance "+req.DataClearance)
		return
	}
	if err := s.global().SetConnectionModelClearance(r.Context(), connID, req.ModelID, req.DataClearance, req.ClearanceNote); err != nil {
		writeErr(w, http.StatusNotFound, "connection model not found (refresh the connection's models first)")
		return
	}
	s.record(r.Context(), actorOf(r), "provider.model_clearance", connID, map[string]string{"model": req.ModelID, "clearance": req.DataClearance})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) activateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.global().GetProvider(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "provider not found")
		return
	}
	built, err := s.buildProvider(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.setProvider(built, infoFor(id, p, built))
	if err := s.global().SetSetting(r.Context(), activeProviderSetting, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "provider.activate", id, map[string]string{"type": p.Type, "model": p.Model})
	writeJSON(w, http.StatusOK, s.activeInfo())
}

func (s *Server) testProvider(w http.ResponseWriter, r *http.Request) {
	p, err := s.global().GetProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "provider not found")
		return
	}
	built, err := s.buildProvider(p)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := built.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "You are a connectivity check. Reply with the single word: OK"},
		{Role: llm.RoleUser, Content: "ping"},
	}})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sample := resp.Text
	if len(sample) > 200 {
		sample = sample[:200]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"latency_ms":      time.Since(start).Milliseconds(),
		"sample":          sample,
		"requested_model": p.Model, // the connection's default model (blank = backend's own default)
		"served_model":    resp.Model,
	})
}

// projectUsage returns a project's Analyst token usage grouped by provider/model, for comparison.
func (s *Server) projectUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.pdb(r).UsageByModel(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if u == nil {
		u = []model.UsageByModel{}
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.global().DeleteProvider(r.Context(), id); err != nil {
		writeErr(w, http.StatusNotFound, "provider not found")
		return
	}
	if s.activeInfo().ID == id {
		_ = s.global().SetSetting(r.Context(), activeProviderSetting, "")
	}
	w.WriteHeader(http.StatusNoContent)
}
