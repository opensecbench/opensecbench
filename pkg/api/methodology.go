package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// listMethodologies returns the full methodology catalog (all packs + items). Each pack is flagged Builtin
// unless it has a saved-pack row (ADR-0055), so the editor shows code-defined and extension packs read-only.
func (s *Server) listMethodologies(w http.ResponseWriter, r *http.Request) {
	saved := s.savedMethodologyIDs(r)
	packs := s.methods.All()
	for i := range packs {
		packs[i].Builtin = !saved[packs[i].ID]
	}
	writeJSON(w, http.StatusOK, packs)
}

// savedMethodologyIDs returns the set of pack ids that are user-authored (editable). A load failure yields an
// empty set, which conservatively marks every pack built-in rather than offering edits that would then fail.
func (s *Server) savedMethodologyIDs(r *http.Request) map[string]bool {
	set := map[string]bool{}
	saved, err := s.global().ListSavedMethodologies(r.Context())
	if err != nil {
		return set
	}
	for _, m := range saved {
		set[m.ID] = true
	}
	return set
}

// createMethodology stores a user-authored methodology pack and registers it so it's immediately adoptable.
func (s *Server) createMethodology(w http.ResponseWriter, r *http.Request) {
	var m methodology.Methodology
	if !decodeJSON(w, r, &m) {
		return
	}
	methodology.Normalize(&m)
	if err := methodology.Validate(m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := s.methods.Get(m.ID); ok {
		writeErr(w, http.StatusBadRequest, "a methodology with id "+m.ID+" already exists; give it a different title")
		return
	}
	if err := methodology.CheckItemCollisions(s.methods, m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.persistMethodology(r, m, true); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.methods.Register(m)
	s.record(r.Context(), actorOf(r), "methodology.create", m.ID, map[string]string{"title": m.Title})
	m.Builtin = false
	writeJSON(w, http.StatusCreated, m)
}

// updateMethodology edits a saved pack in place, keeping its id so adopted-pack and coverage references stay
// valid. Built-in and extension packs have no saved row, so editing one returns 404.
func (s *Server) updateMethodology(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var m methodology.Methodology
	if !decodeJSON(w, r, &m) {
		return
	}
	m.ID = id // the path is authoritative; ids never change on edit
	methodology.Normalize(&m)
	if err := methodology.Validate(m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := methodology.CheckItemCollisions(s.methods, m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_, err := s.persistMethodology(r, m, false)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "methodology not found or not editable (built-ins can't be edited; save a copy instead)")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.methods.Register(m)
	s.record(r.Context(), actorOf(r), "methodology.update", m.ID, map[string]string{"title": m.Title})
	m.Builtin = false
	writeJSON(w, http.StatusOK, m)
}

// deleteMethodology removes a user-saved pack (built-ins can't be deleted) and sweeps its orphaned per-project
// adoption + coverage rows so no project keeps coverage for a pack that no longer exists (ADR-0055).
func (s *Server) deleteMethodology(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Read the pack first so we know its item ids for the coverage sweep; a missing row means it's built-in.
	sm, err := s.global().GetSavedMethodology(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "methodology not found (built-ins can't be deleted)")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var pack methodology.Methodology
	_ = json.Unmarshal(sm.Data, &pack)
	itemIDs := make([]string, 0, len(pack.Items))
	for _, it := range pack.Items {
		itemIDs = append(itemIDs, it.ID)
	}

	if err := s.global().DeleteSavedMethodology(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.methods.Remove(id)
	// Best-effort cleanup: the pack is already gone, so a sweep failure shouldn't fail the delete — orphaned
	// rows are harmless (BuildCoverage skips unknown packs) and can be re-swept later.
	if err := s.mgr.PurgeMethodologyPack(r.Context(), id, itemIDs); err != nil {
		s.record(r.Context(), actorOf(r), "methodology.delete.sweep_failed", id, map[string]string{"error": err.Error()})
	}
	s.record(r.Context(), actorOf(r), "methodology.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// persistMethodology marshals the pack and writes it to the saved-methodology store (create or update).
func (s *Server) persistMethodology(r *http.Request, m methodology.Methodology, create bool) (model.SavedMethodology, error) {
	m.Builtin = false // never persist the transient UI flag
	data, err := json.Marshal(m)
	if err != nil {
		return model.SavedMethodology{}, err
	}
	row := model.SavedMethodology{ID: m.ID, Title: m.Title, Data: data}
	if create {
		return s.global().CreateSavedMethodology(r.Context(), row)
	}
	return s.global().UpdateSavedMethodology(r.Context(), row)
}

// getMethodologyCoverage returns a project's adopted packs with per-item status and a roll-up.
func (s *Server) getMethodologyCoverage(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	adopted, err := s.pdb(r).ListAdoptedMethodologies(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries, err := s.pdb(r).ListCoverage(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	states := make(map[string]methodology.State, len(entries))
	for _, e := range entries {
		states[e.ItemID] = methodology.State{Status: e.Status, Note: e.Note}
	}
	view := methodology.BuildCoverage(s.methods, adopted, states)

	// Enrich items with attached-evidence counts (ADR-0015 P3b). Done here rather than in
	// BuildCoverage so the dependency-free coverage builder and its other callers stay unchanged.
	evidence, err := s.pdb(r).CountCoverageEvidence(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for pi := range view.Packs {
		for ii := range view.Packs[pi].Items {
			view.Packs[pi].Items[ii].EvidenceCount = evidence[view.Packs[pi].Items[ii].Item.ID]
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// methodologySuggestions recommends packs to adopt based on the project's inherited knowledge base.
func (s *Server) methodologySuggestions(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	kb, err := s.mgr.ListKBForProject(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var sb strings.Builder
	for _, e := range kb {
		sb.WriteString(e.Kind)
		sb.WriteByte(' ')
		sb.WriteString(e.Title)
		sb.WriteByte(' ')
		sb.WriteString(e.Body)
		sb.WriteByte(' ')
		sb.WriteString(e.Tags)
		sb.WriteByte('\n')
	}
	adopted, _ := s.pdb(r).ListAdoptedMethodologies(r.Context(), projectID)
	writeJSON(w, http.StatusOK, methodology.Suggest(s.methods, sb.String(), adopted))
}

func (s *Server) adoptMethodology(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var req struct {
		MethodologyID string `json:"methodology_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, ok := s.methods.Get(req.MethodologyID); !ok {
		writeErr(w, http.StatusBadRequest, "unknown methodology "+req.MethodologyID)
		return
	}
	if err := s.pdb(r).AdoptMethodology(r.Context(), projectID, req.MethodologyID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "methodology.adopt", projectID, map[string]string{"methodology": req.MethodologyID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unadoptMethodology(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var req struct {
		MethodologyID string `json:"methodology_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.pdb(r).UnadoptMethodology(r.Context(), projectID, req.MethodologyID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setCoverage records the operator's status + note for a methodology item.
func (s *Server) setCoverage(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var req struct {
		ItemID string `json:"item_id"`
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, _, ok := s.methods.Item(req.ItemID); !ok {
		writeErr(w, http.StatusBadRequest, "unknown methodology item "+req.ItemID)
		return
	}
	if err := s.pdb(r).SetCoverage(r.Context(), projectID, req.ItemID, req.Status, req.Note); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "coverage.set", req.ItemID, map[string]string{"project": projectID, "status": req.Status})
	w.WriteHeader(http.StatusNoContent)
}
