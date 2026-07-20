package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/llm/catalog"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// catalogKey maps a connection type (protocol adapter) to the curated overlay's provider key (ADR-0052).
// The overlay is family metadata, so gateway types that serve many families (bedrock) map to "" and rely
// on per-id family normalization instead of an overlay-by-provider list.
func catalogKey(connType string) string {
	switch strings.ToLower(strings.TrimSpace(connType)) {
	case "anthropic", "claude", "claude-cli", "cli":
		return "anthropic"
	case "openai", "azure", "openai-compat":
		return "openai"
	case "deepseek":
		return "deepseek"
	case "grok", "xai":
		return "grok"
	case "ollama":
		return "ollama"
	default:
		return strings.ToLower(strings.TrimSpace(connType))
	}
}

// enrichDiscovered turns a raw discovered model into a cached ConnectionModel by overlaying curated
// metadata (ADR-0052): an exact (provider, id) catalog hit wins; otherwise the model's normalized family
// supplies context/price/tags shared across connections that serve the same family.
func enrichDiscovered(connID, catKey string, d llm.DiscoveredModel) model.ConnectionModel {
	cm := model.ConnectionModel{ConnectionID: connID, ModelID: d.ID, DisplayName: d.DisplayName, Source: "live"}
	cm.Family = catalog.Family(catKey, d.ID)
	if meta, ok := catalog.MetaForFamily(cm.Family); ok {
		applyMeta(&cm, meta)
	}
	if exact, ok := catalog.Get(catKey, d.ID); ok {
		applyMeta(&cm, exact) // an exact hit is more precise than the family representative
	}
	if cm.DisplayName == "" {
		cm.DisplayName = d.ID
	}
	return cm
}

func applyMeta(cm *model.ConnectionModel, m catalog.Model) {
	cm.ContextWindow = m.ContextWindow
	cm.InputPerMTok = m.InputPerMTok
	cm.OutputPerMTok = m.OutputPerMTok
	cm.Tags = m.DefaultTags
	if cm.Family == "" {
		cm.Family = m.Family
	}
	if cm.DisplayName == "" {
		cm.DisplayName = m.Name
	}
}

// refreshConnectionModels discovers a connection's live model set, enriches it with the overlay, and
// caches it. When discovery isn't available (no lister, no vault key, or the backend fetch fails) it
// falls back to the curated overlay for the connection's provider key so the picker is never empty.
func (s *Server) refreshConnectionModels(ctx context.Context, p model.Provider) ([]model.ConnectionModel, error) {
	catKey := catalogKey(p.Type)
	var out []model.ConnectionModel

	if built, err := s.buildProvider(p); err == nil {
		dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		discovered, attempted, derr := llm.ListModels(dctx, built)
		cancel()
		if attempted && derr == nil {
			for _, d := range discovered {
				out = append(out, enrichDiscovered(p.ID, catKey, d))
			}
		}
	}

	if len(out) == 0 { // overlay fallback — never leave the picker empty
		for _, m := range catalog.ByProvider(catKey) {
			out = append(out, model.ConnectionModel{
				ConnectionID: p.ID, ModelID: m.ID, DisplayName: m.Name, Family: m.Family,
				ContextWindow: m.ContextWindow, InputPerMTok: m.InputPerMTok, OutputPerMTok: m.OutputPerMTok,
				Tags: m.DefaultTags, Source: "overlay",
			})
		}
	}

	if err := s.global().ReplaceConnectionModels(ctx, p.ID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// listConnectionModels returns a connection's cached models, discovering them on first access (ADR-0052).
func (s *Server) listConnectionModels(w http.ResponseWriter, r *http.Request) {
	p, err := s.global().GetProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "connection not found")
		return
	}
	models, err := s.global().ListConnectionModels(r.Context(), p.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(models) == 0 { // never discovered yet — do it lazily
		if models, err = s.refreshConnectionModels(r.Context(), p); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		p, _ = s.global().GetProvider(r.Context(), p.ID)
	}
	writeJSON(w, http.StatusOK, connectionModelsResponse(p, models))
}

// refreshConnectionModelsHandler forces a live re-discovery of a connection's models.
func (s *Server) refreshConnectionModelsHandler(w http.ResponseWriter, r *http.Request) {
	p, err := s.global().GetProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "connection not found")
		return
	}
	models, err := s.refreshConnectionModels(r.Context(), p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ = s.global().GetProvider(r.Context(), p.ID)
	s.record(r.Context(), actorOf(r), "connection.models.refresh", p.ID, map[string]string{"count": strconv.Itoa(len(models))})
	writeJSON(w, http.StatusOK, connectionModelsResponse(p, models))
}

func connectionModelsResponse(p model.Provider, models []model.ConnectionModel) map[string]any {
	if models == nil {
		models = []model.ConnectionModel{}
	}
	var refreshed string
	if !p.ModelsRefreshedAt.IsZero() {
		refreshed = p.ModelsRefreshedAt.Format(time.RFC3339)
	}
	return map[string]any{"models": models, "refreshed_at": refreshed}
}
