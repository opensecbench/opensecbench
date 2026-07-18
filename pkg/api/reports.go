package api

import (
	"bytes"
	"net/http"
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/report"
)

// templateInfo is the JSON view of a report template.
type templateInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

func (s *Server) listReportTemplates(w http.ResponseWriter, _ *http.Request) {
	var out []templateInfo
	for _, t := range s.reports.Templates() {
		out = append(out, templateInfo{ID: t.ID, Title: t.Title, Kind: t.Kind})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listReports(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListReportsByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func reportMediaType(format report.Format) string {
	if format == report.FormatMarkdown {
		return "text/markdown; charset=utf-8"
	}
	return "text/html; charset=utf-8"
}

// generateReport builds a report from a project's confirmed findings, stores the rendered bytes in
// the CAS, and records the report (ADR-0008). Only findings with traceable evidence appear.
func (s *Server) generateReport(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var req struct {
		Template string `json:"template"`
		Format   string `json:"format"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	tmpl, ok := s.reports.Get(req.Template)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown report template "+req.Template)
		return
	}
	format := report.Format(req.Format)
	if format != report.FormatMarkdown && format != report.FormatHTML {
		format = report.FormatHTML
	}

	data, err := report.NewBuilder(s.store).Build(r.Context(), projectID, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "build report: "+err.Error())
		return
	}
	rendered, err := tmpl.Render(data, format)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "render: "+err.Error())
		return
	}

	digest, err := s.cas.Put(bytes.NewReader(rendered))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := tmpl.ID + "." + string(format)
	art, err := s.store.CreateArtifact(r.Context(), model.Artifact{
		SHA256:    digest,
		Size:      int64(len(rendered)),
		Kind:      model.ArtifactOutput,
		Name:      name,
		MediaType: reportMediaType(format),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rep, err := s.store.CreateReport(r.Context(), model.Report{
		ProjectID:  projectID,
		TemplateID: tmpl.ID,
		Format:     string(format),
		Title:      tmpl.Title,
		ArtifactID: art.ID,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "report.generate", rep.ID, map[string]any{
		"template": tmpl.ID, "format": format, "findings": data.Summary.Total,
	})
	writeJSON(w, http.StatusCreated, rep)
}
