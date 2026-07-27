package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/report"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// reportTemplateDetail is the editor's view of a single template: the raw MD/HTML source plus a builtin
// flag. Built-ins have no saved row — the editor loads their source here so a user can fork and tweak.
type reportTemplateDetail struct {
	model.ReportTemplate
	Builtin bool `json:"builtin"`
}

// getReportTemplate returns one template's editable source. A saved (user) template returns its stored
// row; a built-in returns its registered source with Builtin=true so the editor can pre-fill a fork.
func (s *Server) getReportTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if saved, err := s.global().GetReportTemplate(r.Context(), id); err == nil {
		writeJSON(w, http.StatusOK, reportTemplateDetail{ReportTemplate: saved, Builtin: false})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Fall back to a built-in / extension template registered in memory.
	t, ok := s.reports.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "report template not found")
		return
	}
	md, html := t.Source()
	writeJSON(w, http.StatusOK, reportTemplateDetail{
		ReportTemplate: model.ReportTemplate{ID: t.ID, Title: t.Title, Kind: t.Kind, MD: md, HTML: html},
		Builtin:        true,
	})
}

// createReportTemplate persists a user-authored template and registers it so reports can be generated from
// it immediately. The id must not collide with any existing template (built-in or saved) — forking a
// built-in means saving under a new id.
func (s *Server) createReportTemplate(w http.ResponseWriter, r *http.Request) {
	var t model.ReportTemplate
	if !decodeJSON(w, r, &t) {
		return
	}
	normalizeReportTemplate(&t)
	if msg := validateReportTemplate(t); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if _, exists := s.reports.Get(t.ID); exists {
		writeErr(w, http.StatusBadRequest, "a report template with id "+t.ID+" already exists; pick a different title, or edit that template")
		return
	}
	if err := report.Parse(t.ID, t.MD, t.HTML); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.global().SaveReportTemplate(r.Context(), t)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Already parse-validated above, so Add cannot fail here.
	_ = s.reports.Add(t.ID, t.Title, t.Kind, t.MD, t.HTML)
	s.record(r.Context(), actorOf(r), "report_template.create", t.ID, map[string]string{"title": t.Title})
	writeJSON(w, http.StatusCreated, reportTemplateDetail{ReportTemplate: saved, Builtin: false})
}

// updateReportTemplate edits a saved template in place. Built-ins have no saved row, so editing one 404s
// (the caller forks a copy instead), keeping the shipped templates immutable.
func (s *Server) updateReportTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var t model.ReportTemplate
	if !decodeJSON(w, r, &t) {
		return
	}
	t.ID = id // the path is authoritative; ids never change on edit
	normalizeReportTemplate(&t)
	if msg := validateReportTemplate(t); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := report.Parse(t.ID, t.MD, t.HTML); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.global().UpdateReportTemplate(r.Context(), t)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "report template not found or not editable (built-ins can't be edited; save a copy instead)")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.reports.Add(t.ID, t.Title, t.Kind, t.MD, t.HTML) // re-register with the new source
	s.record(r.Context(), actorOf(r), "report_template.update", t.ID, map[string]string{"title": t.Title})
	writeJSON(w, http.StatusOK, reportTemplateDetail{ReportTemplate: saved, Builtin: false})
}

// deleteReportTemplate removes a user-saved template and unregisters it. Built-ins can't be deleted (no row).
func (s *Server) deleteReportTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.global().DeleteReportTemplate(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "report template not found (built-ins can't be deleted)")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reports.Remove(id)
	s.record(r.Context(), actorOf(r), "report_template.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// previewReportTemplate renders draft MD/HTML against a real project's data WITHOUT saving, powering the
// editor's live preview. Only Markdown and HTML render here (PDF/DOCX are a generation concern). The
// rendered bytes are returned raw so the editor can drop HTML into an iframe or show Markdown as text.
func (s *Server) previewReportTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID    string `json:"project_id"`
		Format       string `json:"format"`
		MD           string `json:"md"`
		HTML         string `json:"html"`
		BrandName    string `json:"brand_name"`
		BrandTagline string `json:"brand_tagline"`
		BrandColor   string `json:"brand_color"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		writeErr(w, http.StatusBadRequest, "project_id is required to preview a template against real data")
		return
	}
	if req.MD == "" || req.HTML == "" {
		writeErr(w, http.StatusBadRequest, "md and html are both required")
		return
	}
	format := report.Format(req.Format)
	if format != report.FormatMarkdown {
		format = report.FormatHTML // only md/html preview; anything else renders as HTML
	}
	data, err := report.NewBuilder(s.pdbID(req.ProjectID)).WithMethodology(s.methods).Build(r.Context(), req.ProjectID, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "build report: "+err.Error())
		return
	}
	data.Brand = report.Brand{Name: req.BrandName, Tagline: req.BrandTagline, Color: req.BrandColor}
	rendered, err := report.RenderDraft(req.MD, req.HTML, data, format)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", reportMediaType(format))
	_, _ = w.Write(rendered)
}

// normalizeReportTemplate fills derived fields: an id slugged from the title when absent, and a default
// kind. ids for saved templates are stable once created (the update path forces the path id).
func normalizeReportTemplate(t *model.ReportTemplate) {
	t.Title = strings.TrimSpace(t.Title)
	t.ID = strings.TrimSpace(t.ID)
	if t.ID == "" {
		t.ID = methodology.Slug(t.Title)
	}
	if strings.TrimSpace(t.Kind) == "" {
		t.Kind = "custom"
	}
}

// validateReportTemplate returns a human-readable reason the template is invalid, or "" if it is well-formed.
func validateReportTemplate(t model.ReportTemplate) string {
	if t.ID == "" {
		return "a title (or explicit id) is required"
	}
	if t.Title == "" {
		return "a title is required"
	}
	if strings.TrimSpace(t.MD) == "" || strings.TrimSpace(t.HTML) == "" {
		return "both the Markdown and HTML template bodies are required"
	}
	return ""
}
