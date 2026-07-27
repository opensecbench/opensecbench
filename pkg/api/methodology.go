package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/events"
	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
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

// draftMethodology converts pasted free-form checklist text into a structured methodology pack via the LLM
// WITHOUT persisting it (ADR-0055). The frontend opens the draft in the editor for review and saves it through
// the normal create path — so LLM output always gets a human glance before it enters the catalog.
func (s *Server) draftMethodology(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text  string `json:"text"`
		Title string `json:"title"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, "checklist text is required")
		return
	}
	svc := s.analystService()
	if !svc.Available() {
		writeErr(w, http.StatusServiceUnavailable, "no LLM provider configured")
		return
	}
	m, err := svc.ConvertChecklist(r.Context(), req.Text, req.Title)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
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

// agentCheck is one resolved agent methodology check to run (ADR-0056 P2).
type agentCheck struct {
	itemID      string
	profile     string
	instruction string
}

// methodologyAgentRuns tracks which methodology items currently have an agent check in flight, per project
// (ADR-0056 P2). Agent checks are blocking delegate runs, not engine tasks, so their liveness isn't in the
// tasks table — this in-memory set feeds the coverage view's transient RunState. Process-local (desktop app).
type methodologyAgentRuns struct {
	mu      sync.Mutex
	running map[string]map[string]bool // projectID -> itemID set
}

func newMethodologyAgentRuns() *methodologyAgentRuns {
	return &methodologyAgentRuns{running: map[string]map[string]bool{}}
}

func (m *methodologyAgentRuns) mark(projectID, itemID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[projectID] == nil {
		m.running[projectID] = map[string]bool{}
	}
	m.running[projectID][itemID] = true
}

func (m *methodologyAgentRuns) clear(projectID, itemID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.running[projectID]; s != nil {
		delete(s, itemID)
		if len(s) == 0 {
			delete(m.running, projectID)
		}
	}
}

func (m *methodologyAgentRuns) states(projectID string) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for id := range m.running[projectID] {
		out[id] = "running"
	}
	return out
}

// runMethodology fans out a project's adopted-pack checks (ADR-0056). Optional query params narrow the run:
// ?pack=<id> to one pack, ?item=<id> to one item. Capability checks go through the engine; agent checks run
// as background sub-agents (P2); manual checks are deferred to human sign-off (P3, reported). Returns the run
// id and what was enqueued/started/skipped.
func (s *Server) runMethodology(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeErr(w, http.StatusServiceUnavailable, "engine unavailable")
		return
	}
	projectID := r.PathValue("id")
	onlyPack := r.URL.Query().Get("pack")
	onlyItem := r.URL.Query().Get("item")

	adopted, err := s.pdb(r).ListAdoptedMethodologies(r.Context(), projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var capChecks []task.MethodologyCheck
	var agentChecks []agentCheck
	manual := 0
	for _, packID := range adopted {
		if onlyPack != "" && onlyPack != packID {
			continue
		}
		pack, ok := s.methods.Get(packID)
		if !ok {
			continue
		}
		for _, it := range pack.Items {
			if onlyItem != "" && onlyItem != it.ID {
				continue
			}
			for _, c := range methodology.EffectiveChecks(it) {
				switch c.Kind {
				case methodology.CheckCapability:
					capChecks = append(capChecks, task.MethodologyCheck{ItemID: it.ID, CapabilityID: c.Capability})
				case methodology.CheckAgent:
					agentChecks = append(agentChecks, agentCheck{itemID: it.ID, profile: c.Profile, instruction: c.Instruction})
				case methodology.CheckManual:
					manual++
				}
			}
		}
	}
	if len(capChecks) == 0 && len(agentChecks) == 0 {
		writeErr(w, http.StatusBadRequest, "nothing to run: the adopted packs have no capability or agent checks (manual checks need human sign-off)")
		return
	}
	runID := uuid.NewString()

	res, err := s.engine.RunMethodologyChecks(r.Context(), projectID, runID, capChecks)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Agent checks run as background sub-agents (blocking delegate runs); skip them cleanly with no provider.
	agentStarted := 0
	if len(agentChecks) > 0 && s.analystService().Available() {
		s.runMethodologyAgentChecks(projectID, runID, agentChecks)
		agentStarted = len(agentChecks)
	}

	s.record(r.Context(), actorOf(r), "methodology.run", projectID, map[string]string{"run": runID})
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id":          runID,
		"enqueued":        len(res.Enqueued),
		"agent_started":   agentStarted,
		"deferred_manual": manual,
		"skipped":         res.Skipped,
	})
}

// runMethodologyAgentChecks runs each agent check as a background sub-agent (ADR-0056 P2). Each delegates to
// the item's profile with its instruction, carrying the item id so the sub-agent's observations attach to the
// item as evidence; on completion it flips coverage (covered, or in_progress if the run errored/stopped) — the
// agent driving coverage exactly as a human would. Concurrency is bounded inside Delegate (agentSem).
func (s *Server) runMethodologyAgentChecks(projectID, runID string, checks []agentCheck) {
	svc := s.analystService()
	for _, c := range checks {
		s.methAgents.mark(projectID, c.itemID)
		s.events.Publish(events.Event{Type: "methodology.item", ProjectID: projectID, Payload: map[string]any{
			"item_id": c.itemID, "run_id": runID, "status": "running", "kind": "agent",
		}})
		go func(c agentCheck) {
			defer s.methAgents.clear(projectID, c.itemID)
			ctx := context.Background() // outlive the HTTP request; the run is cancelable via the RunRegistry
			res, err := svc.RunMethodologyAgentCheck(ctx, projectID, c.itemID, c.profile, c.instruction)
			status, note := model.CoverageCovered, "methodology run · agent:"+c.profile
			if err != nil {
				status, note = model.CoverageInProgress, "methodology run · agent:"+c.profile+" (error)"
			} else if res.Stopped {
				status, note = model.CoverageInProgress, "methodology run · agent:"+c.profile+" (incomplete)"
			}
			if pdb, e := s.mgr.Project(projectID); e == nil && pdb != nil {
				_ = pdb.SetCoverage(ctx, projectID, c.itemID, status, note)
			}
			s.events.Publish(events.Event{Type: "methodology.item", ProjectID: projectID, Payload: map[string]any{
				"item_id": c.itemID, "run_id": runID, "status": status, "kind": "agent",
			}})
		}(c)
	}
}

// methodologyOnComplete is the engine's on-complete hook (ADR-0056): when a methodology-triggered task
// finishes, attach its observations to the originating item as evidence and flip the item's coverage to
// tested (in_progress if the task failed). Findings are a separate signal, so coverage means "we tested
// this," not "it's clean." A human and an agent both drive coverage identically (ADR-0053/0054) — this hook
// is indifferent to who produced the observations.
func (s *Server) methodologyOnComplete(ctx context.Context, oc task.Outcome) {
	t := oc.Task
	if t.MethodologyItemID == nil || *t.MethodologyItemID == "" || t.ProjectID == nil || *t.ProjectID == "" {
		return
	}
	itemID, projectID := *t.MethodologyItemID, *t.ProjectID
	pdb, err := s.mgr.Project(projectID)
	if err != nil || pdb == nil {
		return
	}
	for _, o := range oc.Observations {
		_ = pdb.LinkCoverageObservation(ctx, projectID, itemID, o.ID)
	}
	status, note := model.CoverageCovered, "methodology run · "+t.CapabilityID
	if t.Status != model.TaskSucceeded {
		status, note = model.CoverageInProgress, "methodology run · "+t.CapabilityID+" ("+t.Status+")"
	}
	_ = pdb.SetCoverage(ctx, projectID, itemID, status, note)
	runID := ""
	if t.MethodologyRunID != nil {
		runID = *t.MethodologyRunID
	}
	s.events.Publish(events.Event{Type: "methodology.item", ProjectID: projectID, Payload: map[string]any{
		"item_id": itemID, "status": status, "run_id": runID, "capability": t.CapabilityID, "task_status": t.Status,
	}})
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
	// Live run state (ADR-0056): mark items with an in-flight methodology task as queued/running so the
	// control panel shows the run in progress. Best-effort — a failure just omits the transient state.
	active, _ := s.pdb(r).ActiveMethodologyItemStates(r.Context(), projectID)
	for id, st := range s.methAgents.states(projectID) { // agent checks aren't tasks; overlay their liveness
		active[id] = st
	}
	// Findings linked to each item (the "what we found" signal, separate from coverage) — ADR-0056 P3.
	findings, _ := s.pdb(r).FindingsByMethodologyItem(r.Context(), projectID)
	for pi := range view.Packs {
		for ii := range view.Packs[pi].Items {
			id := view.Packs[pi].Items[ii].Item.ID
			view.Packs[pi].Items[ii].EvidenceCount = evidence[id]
			view.Packs[pi].Items[ii].RunState = active[id]
			if f, ok := findings[id]; ok {
				view.Packs[pi].Items[ii].FindingCount = f.Count
				view.Packs[pi].Items[ii].FindingSeverity = f.WorstSeverity
			}
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
