package api

import (
	"errors"
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/scope"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// --- Asset extension endpoints (ADR-0071) ---

func (s *Server) setAssetTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tags []string `json:"tags"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	asset, err := s.pdb(r).SetAssetTags(r.Context(), r.PathValue("id"), req.Tags)
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

func (s *Server) setAssetStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	asset, err := s.pdb(r).UpdateAssetStatus(r.Context(), r.PathValue("id"), req.Status)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

func (s *Server) setAssetVerification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	asset, err := s.pdb(r).UpdateAssetVerification(r.Context(), r.PathValue("id"), req.State)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

// --- Scope import ---

func (s *Server) importScope(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Text == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return
	}
	result := scope.ParseScopeDocument(req.Text)
	writeJSON(w, http.StatusOK, result)
}

// --- Entity links ---

func (s *Server) listLinks(w http.ResponseWriter, r *http.Request) {
	entityType := r.URL.Query().Get("type")
	entityID := r.URL.Query().Get("id")
	if entityType == "" || entityID == "" {
		writeErr(w, http.StatusBadRequest, "query params type and id required")
		return
	}
	links, err := s.pdb(r).ListLinks(r.Context(), entityType, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *Server) createLink(w http.ResponseWriter, r *http.Request) {
	var req model.EntityLink
	if !decodeJSON(w, r, &req) {
		return
	}
	link, err := s.pdb(r).CreateLink(r.Context(), req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, link)
}

func (s *Server) deleteLink(w http.ResponseWriter, r *http.Request) {
	err := s.pdb(r).DeleteLink(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "link not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Research items ---

func (s *Server) listResearchItems(w http.ResponseWriter, r *http.Request) {
	items, err := s.pdb(r).ListResearchItems(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createResearchItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type       string   `json:"type"`
		Title      string   `json:"title"`
		Body       string   `json:"body"`
		Status     string   `json:"status"`
		Assessment string   `json:"assessment"`
		CreatedBy  string   `json:"created_by"`
		Tags       []string `json:"tags"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := s.pdb(r).CreateResearchItem(r.Context(), store.NewResearchItem{
		ProjectID:  r.PathValue("id"),
		Type:       req.Type,
		Title:      req.Title,
		Body:       req.Body,
		Status:     req.Status,
		Assessment: req.Assessment,
		CreatedBy:  req.CreatedBy,
		Tags:       req.Tags,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getResearchItem(w http.ResponseWriter, r *http.Request) {
	item, err := s.pdb(r).GetResearchItem(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "research item not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateResearchItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title      *string   `json:"title"`
		Body       *string   `json:"body"`
		Status     *string   `json:"status"`
		Assessment *string   `json:"assessment"`
		Tags       *[]string `json:"tags"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := s.pdb(r).UpdateResearchItem(r.Context(), r.PathValue("id"), store.ResearchItemUpdate{
		Title:      req.Title,
		Body:       req.Body,
		Status:     req.Status,
		Assessment: req.Assessment,
		Tags:       req.Tags,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "research item not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteResearchItem(w http.ResponseWriter, r *http.Request) {
	err := s.pdb(r).DeleteResearchItem(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "research item not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
