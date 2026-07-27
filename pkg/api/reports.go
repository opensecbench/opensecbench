package api

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/report"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// templateInfo is the JSON view of a report template. Builtin flags the code-defined/extension templates
// (no saved row) so the editor knows which are read-only — editing one forks a copy instead.
type templateInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Builtin bool   `json:"builtin"`
}

func (s *Server) listReportTemplates(w http.ResponseWriter, r *http.Request) {
	saved := map[string]bool{}
	if rows, err := s.global().ListReportTemplates(r.Context()); err == nil {
		for _, t := range rows {
			saved[t.ID] = true
		}
	}
	out := []templateInfo{}
	for _, t := range s.reports.Templates() {
		out = append(out, templateInfo{ID: t.ID, Title: t.Title, Kind: t.Kind, Builtin: !saved[t.ID]})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listReports(w http.ResponseWriter, r *http.Request) {
	items, err := s.pdb(r).ListReportsByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// deleteReport retires a generated report from the project's list.
func (s *Server) deleteReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.pdb(r).DeleteReport(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "report not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "report.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func reportMediaType(format report.Format) string {
	switch format {
	case report.FormatMarkdown:
		return "text/markdown; charset=utf-8"
	case report.FormatPDF:
		return "application/pdf"
	case report.FormatDOCX:
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	default:
		return "text/html; charset=utf-8"
	}
}

// generateReport builds a report from a project's confirmed findings, stores the rendered bytes in
// the CAS, and records the report (ADR-0008). Only findings with traceable evidence appear.
func (s *Server) generateReport(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var req struct {
		Template     string `json:"template"`
		Format       string `json:"format"`
		BrandName    string `json:"brand_name"`
		BrandTagline string `json:"brand_tagline"`
		BrandColor   string `json:"brand_color"`
		Narrate      bool   `json:"narrate"` // author an executive summary + per-finding impact/remediation (ADR-0045)
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
	switch format {
	case report.FormatMarkdown, report.FormatHTML, report.FormatPDF, report.FormatDOCX:
	default:
		format = report.FormatHTML
	}

	data, err := report.NewBuilder(s.pdb(r)).WithMethodology(s.methods).Build(r.Context(), projectID, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "build report: "+err.Error())
		return
	}
	data.Brand = report.Brand{Name: req.BrandName, Tagline: req.BrandTagline, Color: req.BrandColor}

	// Agent-authored narrative (ADR-0045): when asked, the analyst writes an executive summary + per-finding
	// impact/remediation grounded in the reportable findings. Best-effort — a narration failure or missing
	// provider degrades to the data-only report rather than failing generation.
	if req.Narrate {
		if svc := s.analystService(); svc != nil && svc.Available() {
			if n, nerr := svc.Narrate(r.Context(), data, report.AudienceFor(tmpl.ID)); nerr == nil {
				data.ApplyNarrative(n)
			} else {
				log.Printf("report narration failed for %s: %v", projectID, nerr)
			}
		}
	}

	// DOCX is generated directly from Data (no browser); PDF is the HTML render printed headless.
	if format == report.FormatDOCX {
		docx, derr := report.DOCX(tmpl.Title, data)
		if derr != nil {
			writeErr(w, http.StatusInternalServerError, "docx: "+derr.Error())
			return
		}
		s.storeReport(w, r, projectID, tmpl, format, docx, data.Summary.Total)
		return
	}
	renderFormat := format
	if format == report.FormatPDF {
		renderFormat = report.FormatHTML
	}
	rendered, err := tmpl.Render(data, renderFormat)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "render: "+err.Error())
		return
	}
	if format == report.FormatPDF {
		browserBin, _ := s.global().GetSetting(r.Context(), "runtime.browser")
		pdf, perr := report.HTMLToPDF(r.Context(), rendered, browserBin)
		if errors.Is(perr, report.ErrNoBrowser) {
			writeErr(w, http.StatusServiceUnavailable, perr.Error())
			return
		}
		if perr != nil {
			writeErr(w, http.StatusInternalServerError, "pdf: "+perr.Error())
			return
		}
		rendered = pdf
	}

	s.storeReport(w, r, projectID, tmpl, format, rendered, data.Summary.Total)
}

// storeReport writes rendered report bytes to the CAS, records the report, audits, notifies, and
// responds. Shared by every format path.
func (s *Server) storeReport(w http.ResponseWriter, r *http.Request, projectID string, tmpl *report.Template, format report.Format, rendered []byte, findings int) {
	digest, err := s.casFor(projectID).Put(bytes.NewReader(rendered))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	art, err := s.pdb(r).CreateArtifact(r.Context(), model.Artifact{
		SHA256:    digest,
		Size:      int64(len(rendered)),
		Kind:      model.ArtifactOutput,
		Name:      tmpl.ID + "." + string(format),
		MediaType: reportMediaType(format),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rep, err := s.pdb(r).CreateReport(r.Context(), model.Report{
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
		"template": tmpl.ID, "format": format, "findings": findings,
	})
	pid := projectID
	s.notify(r.Context(), model.NotifyReport, "Report ready",
		tmpl.Title+" ("+string(format)+") for "+projectID, &pid, "report:"+rep.ID)
	writeJSON(w, http.StatusCreated, rep)
}
