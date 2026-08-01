package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/action"
	"github.com/opensecbench/opensecbench/pkg/events"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// Custom actions (ADR-0059): CRUD for the global action definitions, plus running one against a finding
// or observation. A run is dispatched in the background (an agent action can take many tool turns), so the
// handler records a "running" action.Run, returns it immediately, and a goroutine finishes it and streams
// terminal status on the events hub — the same shape as an async methodology check.

// listActions returns the built-in example actions merged with the user-authored ones.
func (s *Server) listActions(w http.ResponseWriter, r *http.Request) {
	out := action.BuiltIns()
	for i := range out {
		out[i].Builtin = true
	}
	saved, err := s.global().ListActions(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out = append(out, saved...)
	writeJSON(w, http.StatusOK, out)
}

// createAction saves a new user-authored action.
func (s *Server) createAction(w http.ResponseWriter, r *http.Request) {
	var a action.Action
	if !decodeJSON(w, r, &a) {
		return
	}
	a.ID = "" // server assigns
	a.Builtin = false
	saved, err := s.global().CreateAction(r.Context(), a)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "action.create", saved.ID, map[string]any{"name": saved.Name, "kind": saved.Kind})
	writeJSON(w, http.StatusCreated, saved)
}

// updateAction edits a user-authored action. Built-ins are read-only — clone to customize.
func (s *Server) updateAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := action.Get(id); ok {
		writeErr(w, http.StatusBadRequest, "built-in actions can't be edited — clone it to customize")
		return
	}
	var a action.Action
	if !decodeJSON(w, r, &a) {
		return
	}
	a.ID = id
	a.Builtin = false
	saved, err := s.global().UpdateAction(r.Context(), a)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "action not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "action.update", saved.ID, map[string]any{"name": saved.Name})
	writeJSON(w, http.StatusOK, saved)
}

// deleteAction removes a user-authored action.
func (s *Server) deleteAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.global().DeleteAction(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "action not found (built-ins can't be deleted)")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "action.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// resolveAction returns a built-in or a saved action by id.
func (s *Server) resolveAction(ctx context.Context, id string) (action.Action, error) {
	if a, ok := action.Get(id); ok {
		a.Builtin = true
		return a, nil
	}
	return s.global().GetAction(ctx, id)
}

// runFindingAction / runObservationAction run an action against a subject.
func (s *Server) runFindingAction(w http.ResponseWriter, r *http.Request) {
	s.runSubjectAction(w, r, action.SubjectFinding, r.PathValue("id"), r.PathValue("actionId"))
}

func (s *Server) runObservationAction(w http.ResponseWriter, r *http.Request) {
	s.runSubjectAction(w, r, action.SubjectObservation, r.PathValue("id"), r.PathValue("actionId"))
}

// actionProjectID resolves the active project for an action route from the header or query only — NOT the
// path, because on these routes {id} is the subject (finding/observation), not the project, so the
// projectFromReq path fallback would mistake the subject id for a project.
func actionProjectID(r *http.Request) string {
	if h := r.Header.Get("X-Project-Id"); h != "" {
		return h
	}
	return r.URL.Query().Get("project")
}

func (s *Server) runSubjectAction(w http.ResponseWriter, r *http.Request, subjectKind, subjectID, actionID string) {
	projectID := actionProjectID(r)
	if projectID == "" {
		writeErr(w, http.StatusBadRequest, "a project must be selected to run an action")
		return
	}
	db := s.pdbID(projectID)

	a, err := s.resolveAction(r.Context(), actionID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "action not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	subj, err := s.buildSubject(r.Context(), db, projectID, subjectKind, subjectID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, subjectKind+" not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !a.AppliesTo(subj) {
		writeErr(w, http.StatusBadRequest, "this action does not apply to this "+subjectKind)
		return
	}

	run, err := db.CreateActionRun(r.Context(), action.Run{
		ActionID: a.ID, ActionName: a.Name, Kind: a.Kind,
		SubjectKind: subjectKind, SubjectID: subjectID, Status: action.RunRunning,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "action.run", a.ID, map[string]any{"subject_kind": subjectKind, "subject_id": subjectID, "run_id": run.ID})
	s.publishActionRun(projectID, run)

	// Run in the background: an agent action can take many tool turns. The request returns the "running"
	// run; the goroutine finishes it and publishes terminal status.
	go s.executeActionRun(projectID, a, subj, run.ID)

	writeJSON(w, http.StatusAccepted, run)
}

// executeActionRun runs the action to completion, records the terminal run, and publishes it.
func (s *Server) executeActionRun(projectID string, a action.Action, subj action.Subject, runID string) {
	ctx := context.Background()
	db := s.pdbID(projectID)
	output, artifactID, err := s.analystService().ExecuteAction(ctx, projectID, a, subj)

	status, summary, runErr := action.RunDone, summarize(output), ""
	if err != nil {
		status, runErr = action.RunError, err.Error()
		if summary == "" {
			summary = runErr
		}
	}
	if e := db.FinishActionRun(ctx, runID, status, summary, output, artifactID, runErr); e != nil {
		// The run row is best-effort history; log-and-continue rather than lose the event.
		log.Printf("action run %s: finish failed: %v", runID, e)
	}
	if run, e := db.GetActionRun(ctx, runID); e == nil {
		s.publishActionRun(projectID, run)
	}
}

// listSubjectActionRuns returns a subject's action-run history (used by the UI to poll for completion).
func (s *Server) listFindingActionRuns(w http.ResponseWriter, r *http.Request) {
	s.listSubjectActionRuns(w, r, action.SubjectFinding, r.PathValue("id"))
}

func (s *Server) listObservationActionRuns(w http.ResponseWriter, r *http.Request) {
	s.listSubjectActionRuns(w, r, action.SubjectObservation, r.PathValue("id"))
}

func (s *Server) listSubjectActionRuns(w http.ResponseWriter, r *http.Request, subjectKind, subjectID string) {
	projectID := actionProjectID(r)
	if projectID == "" {
		writeErr(w, http.StatusBadRequest, "a project must be selected")
		return
	}
	runs, err := s.pdbID(projectID).ListActionRunsBySubject(r.Context(), subjectKind, subjectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []action.Run{}
	}
	writeJSON(w, http.StatusOK, runs)
}

// buildSubject loads a finding or observation and normalizes it into the templating/applicability view,
// stamping the engagement environment.
func (s *Server) buildSubject(ctx context.Context, db *store.DB, projectID, subjectKind, subjectID string) (action.Subject, error) {
	var subj action.Subject
	switch subjectKind {
	case action.SubjectFinding:
		f, err := db.GetFinding(ctx, subjectID)
		if err != nil {
			return action.Subject{}, err
		}
		loc := ""
		for _, oid := range f.ObservationIDs {
			if o, e := db.GetObservation(ctx, oid); e == nil && o.Location != "" {
				loc = o.Location
				break
			}
		}
		subj = action.Subject{Kind: action.SubjectFinding, ID: f.ID, Title: f.Title, Severity: f.Severity,
			Status: f.Status, CWE: f.CWE, Description: f.Description, Location: loc}
	case action.SubjectObservation:
		o, err := db.GetObservation(ctx, subjectID)
		if err != nil {
			return action.Subject{}, err
		}
		subj = action.Subject{Kind: action.SubjectObservation, ID: o.ID, Title: o.Title, Severity: o.Severity,
			Status: o.ReviewState, CWE: o.Attributes["cwe"], Description: o.Detail, Location: o.Location}
	default:
		return action.Subject{}, errors.New("unknown subject kind")
	}
	if eng, err := db.GetEngagement(ctx, projectID); err == nil {
		subj.Environment = eng.Environment
	}
	return subj, nil
}

func (s *Server) publishActionRun(projectID string, run action.Run) {
	if s.events == nil {
		return
	}
	s.events.Publish(events.Event{Type: "action.run", ProjectID: projectID, Payload: map[string]any{
		"run_id": run.ID, "action_id": run.ActionID, "status": run.Status,
		"subject_kind": run.SubjectKind, "subject_id": run.SubjectID,
	}})
}

// summarize takes the first non-empty line of an output for the run summary.
func summarize(output string) string {
	const max = 240
	for _, line := range strings.Split(output, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return truncateRunes(t, max)
		}
	}
	return truncateRunes(strings.TrimSpace(output), max)
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
