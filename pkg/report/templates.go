package report

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"sort"
	"strings"
	texttemplate "text/template"
)

// Template renders report Data to Markdown or HTML. Built-ins ship first-party in the shape an
// extension package will later provide (ADR-0003, ADR-0008).
type Template struct {
	ID    string
	Title string
	Kind  string

	md   *texttemplate.Template
	html *htmltemplate.Template
}

// Render produces the report in the requested format.
func (t *Template) Render(d Data, format Format) ([]byte, error) {
	var buf bytes.Buffer
	switch format {
	case FormatMarkdown:
		if err := t.md.Execute(&buf, d); err != nil {
			return nil, err
		}
	case FormatHTML:
		if err := t.html.Execute(&buf, d); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("report: unknown format %q", format)
	}
	return buf.Bytes(), nil
}

// Registry holds the available report templates by id.
type Registry struct{ tmpls map[string]*Template }

// Get returns a template by id.
func (r *Registry) Get(id string) (*Template, bool) { t, ok := r.tmpls[id]; return t, ok }

// Templates returns all templates sorted by id.
func (r *Registry) Templates() []*Template {
	out := make([]*Template, 0, len(r.tmpls))
	for _, t := range r.tmpls {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var funcs = map[string]any{
	"upper":  strings.ToUpper,
	"date":   func(t interface{ Format(string) string }) string { return t.Format("2006-01-02 15:04 MST") },
	"sevs":   func() []string { return []string{"critical", "high", "medium", "low", "info"} },
	"sevfmt": func(s string) string { return strings.ToUpper(s) },
	// retest helpers: partition reportable findings by remediation status.
	"remediated": func(fs []Finding) []Finding { return filterStatus(fs, "remediated") },
	"stillopen":  func(fs []Finding) []Finding { return filterStatus(fs, "open", "confirmed") },
	"accepted":   func(fs []Finding) []Finding { return filterStatus(fs, "accepted") },
	"count":      func(fs []Finding) int { return len(fs) },
	"cwegroups":  CWEGroups,
}

func filterStatus(fs []Finding, statuses ...string) []Finding {
	want := map[string]bool{}
	for _, s := range statuses {
		want[s] = true
	}
	var out []Finding
	for _, f := range fs {
		if want[f.Status] {
			out = append(out, f)
		}
	}
	return out
}

// BuiltIns returns the first-party report templates.
func BuiltIns() *Registry {
	r := &Registry{tmpls: map[string]*Template{}}
	r.tmpls["executive"] = mustTemplate("executive", "Executive summary", "exec_summary", execMD, execHTML)
	r.tmpls["technical"] = mustTemplate("technical", "Technical report", "technical", techMD, techHTML)
	r.tmpls["retest"] = mustTemplate("retest", "Retest report", "retest", retestMD, retestHTML)
	r.tmpls["compliance"] = mustTemplate("compliance", "Compliance mapping (CWE)", "compliance_standard", complianceMD, complianceHTML)
	r.tmpls["branded"] = mustTemplate("branded", "Client-branded report", "branded", brandedMD, brandedHTML)
	return r
}

func mustTemplate(id, title, kind, md, html string) *Template {
	return &Template{
		ID:    id,
		Title: title,
		Kind:  kind,
		md:    texttemplate.Must(texttemplate.New(id).Funcs(funcs).Parse(md)),
		html:  htmltemplate.Must(htmltemplate.New(id).Funcs(htmltemplate.FuncMap(funcs)).Parse(html)),
	}
}

const execMD = `# {{.Project.Name}} — Executive Summary

_Generated {{date .GeneratedAt}}_

## Overview

This engagement covered **{{.Summary.Applications}}** application(s) and **{{.Summary.Assets}}** asset(s),
running **{{.Summary.TasksRun}}** analysis task(s). **{{.Summary.Total}}** finding(s) with supporting
evidence are reported below.
{{if .Methodology.Summary.Total}}
Methodology coverage: **{{.Methodology.Summary.CoveredPct}}%** — {{.Methodology.Summary.Covered}} of {{.Methodology.Summary.Total}} items covered ({{.Methodology.Summary.NotApplicable}} n/a).
{{end}}

## Findings by severity

| Severity | Count |
|----------|-------|
{{- range sevs}}
| {{sevfmt .}} | {{index $.Summary.BySeverity .}} |
{{- end}}

## Summary of findings

{{if .Findings}}{{range .Findings}}- **[{{sevfmt .Severity}}]** {{.Title}}{{if .CWE}} ({{.CWE}}){{end}} — _{{.AppName}}_
{{end}}{{else}}_No reportable findings._
{{end}}`

const techMD = `# {{.Project.Name}} — Technical Report

_Generated {{date .GeneratedAt}}_

## Scope

{{if .Scope}}{{range .Scope}}- {{.Kind}}: {{.Value}}
{{end}}{{else}}_No scope allowlist defined (all targets permitted)._
{{end}}
## Coverage

- Applications: {{.Summary.Applications}}
- Assets: {{.Summary.Assets}}
- Tasks run: {{.Summary.TasksRun}}
- Capabilities exercised: {{if .Summary.Capabilities}}{{range $i, $c := .Summary.Capabilities}}{{if $i}}, {{end}}{{$c}}{{end}}{{else}}none{{end}}

{{if .Methodology.Summary.Total}}## Methodology coverage

**{{.Methodology.Summary.CoveredPct}}%** covered ({{.Methodology.Summary.Covered}}/{{.Methodology.Summary.Total}}; {{.Methodology.Summary.NotApplicable}} n/a).

{{range .Methodology.Packs}}### {{.Title}}

{{range .Items}}- [{{.Status}}] {{.Item.Title}}
{{end}}
{{end}}{{end}}## Findings

{{if .Findings}}{{range .Findings}}### [{{sevfmt .Severity}}] {{.Title}}

- **Application:** {{.AppName}}
- **Status:** {{.Status}}{{if .CWE}}
- **CWE:** {{.CWE}}{{end}}

{{if .Description}}{{.Description}}
{{end}}
**Evidence:**

{{range .Evidence}}- {{.Title}}{{if .Location}} — ` + "`{{.Location}}`" + `{{end}} _(origin: {{.Origin}}, {{.ReviewState}})_
{{end}}
{{end}}{{else}}_No reportable findings._
{{end}}`

const retestMD = `# {{.Project.Name}} — Retest Report

_Generated {{date .GeneratedAt}}_

Remediation status of previously reported findings.

- **Remediated:** {{count (remediated .Findings)}}
- **Still open:** {{count (stillopen .Findings)}}
- **Accepted risk:** {{count (accepted .Findings)}}

## ✅ Remediated

{{$r := remediated .Findings}}{{if $r}}{{range $r}}- **[{{sevfmt .Severity}}]** {{.Title}} — _{{.AppName}}_
{{end}}{{else}}_None._
{{end}}
## ❌ Still open

{{$o := stillopen .Findings}}{{if $o}}{{range $o}}- **[{{sevfmt .Severity}}]** {{.Title}}{{if .CWE}} ({{.CWE}}){{end}} — _{{.AppName}}_
{{end}}{{else}}_None._
{{end}}
## ⚠ Accepted risk

{{$a := accepted .Findings}}{{if $a}}{{range $a}}- **[{{sevfmt .Severity}}]** {{.Title}} — _{{.AppName}}_
{{end}}{{else}}_None._
{{end}}`

const retestHTML = `<!doctype html><html><head><meta charset="utf-8">
<title>{{.Project.Name}} — Retest Report</title>
<style>` + reportCSS + `</style></head><body>
<h1>{{.Project.Name}}<span class="sub">Retest Report</span></h1>
<p class="meta">Generated {{date .GeneratedAt}}</p>
<p>Remediation status of previously reported findings:
<b>{{count (remediated .Findings)}}</b> remediated ·
<b>{{count (stillopen .Findings)}}</b> still open ·
<b>{{count (accepted .Findings)}}</b> accepted.</p>
<h2>✅ Remediated</h2>
{{$r := remediated .Findings}}{{if $r}}<ul class="findlist">{{range $r}}<li><span class="sev sev-{{.Severity}}">{{sevfmt .Severity}}</span> {{.Title}} <span class="app">{{.AppName}}</span></li>{{end}}</ul>{{else}}<p><em>None.</em></p>{{end}}
<h2>❌ Still open</h2>
{{$o := stillopen .Findings}}{{if $o}}<ul class="findlist">{{range $o}}<li><span class="sev sev-{{.Severity}}">{{sevfmt .Severity}}</span> {{.Title}}{{if .CWE}} <span class="cwe">{{.CWE}}</span>{{end}} <span class="app">{{.AppName}}</span></li>{{end}}</ul>{{else}}<p><em>None.</em></p>{{end}}
<h2>⚠ Accepted risk</h2>
{{$a := accepted .Findings}}{{if $a}}<ul class="findlist">{{range $a}}<li><span class="sev sev-{{.Severity}}">{{sevfmt .Severity}}</span> {{.Title}} <span class="app">{{.AppName}}</span></li>{{end}}</ul>{{else}}<p><em>None.</em></p>{{end}}
</body></html>`

const complianceMD = `# {{.Project.Name}} — Compliance Mapping (CWE)

_Generated {{date .GeneratedAt}}_

Reportable findings grouped by CWE weakness class.

{{range cwegroups .Findings}}## {{.CWE}}

{{range .Findings}}- **[{{sevfmt .Severity}}]** {{.Title}} — _{{.AppName}}_ ({{.Status}})
{{end}}
{{else}}_No reportable findings._
{{end}}`

const complianceHTML = `<!doctype html><html><head><meta charset="utf-8">
<title>{{.Project.Name}} — Compliance Mapping</title>
<style>` + reportCSS + `</style></head><body>
<h1>{{.Project.Name}}<span class="sub">Compliance Mapping (CWE)</span></h1>
<p class="meta">Generated {{date .GeneratedAt}}</p>
<p>Reportable findings grouped by CWE weakness class.</p>
{{range cwegroups .Findings}}<div class="finding">
<h3>{{.CWE}}</h3>
<ul class="findlist">{{range .Findings}}<li><span class="sev sev-{{.Severity}}">{{sevfmt .Severity}}</span> {{.Title}} <span class="cwe">{{.Status}}</span> <span class="app">{{.AppName}}</span></li>{{end}}</ul>
</div>{{else}}<p><em>No reportable findings.</em></p>{{end}}
</body></html>`

const brandedMD = `# {{if .Brand.Name}}{{.Brand.Name}} — {{end}}Security Assessment: {{.Project.Name}}

{{if .Brand.Tagline}}_{{.Brand.Tagline}}_{{end}}

_Generated {{date .GeneratedAt}}{{if .Brand.Name}} · Prepared by {{.Brand.Name}}{{end}}_

## Summary

**{{.Summary.Total}}** finding(s) with supporting evidence across **{{.Summary.Applications}}**
application(s).{{if .Methodology.Summary.Total}} Methodology coverage: **{{.Methodology.Summary.CoveredPct}}%**.{{end}}

| Severity | Count |
|----------|-------|
{{- range sevs}}
| {{sevfmt .}} | {{index $.Summary.BySeverity .}} |
{{- end}}

## Findings

{{if .Findings}}{{range .Findings}}### [{{sevfmt .Severity}}] {{.Title}}

_{{.AppName}} · {{.Status}}{{if .CWE}} · {{.CWE}}{{end}}_

{{if .Description}}{{.Description}}
{{end}}
{{range .Evidence}}- {{.Title}}{{if .Location}} — ` + "`{{.Location}}`" + `{{end}}
{{end}}
{{end}}{{else}}_No reportable findings._
{{end}}`

const brandedHTML = `<!doctype html><html><head><meta charset="utf-8">
<title>{{if .Brand.Name}}{{.Brand.Name}} — {{end}}{{.Project.Name}}</title>
<style>` + reportCSS + `
.brandbar{border-top:6px solid {{if .Brand.Color}}{{.Brand.Color}}{{else}}#4aa8ff{{end}};padding-top:16px}
.brandname{font-weight:800;color:{{if .Brand.Color}}{{.Brand.Color}}{{else}}#4aa8ff{{end}}}
</style></head><body>
<div class="brandbar">
{{if .Brand.Name}}<div class="brandname">{{.Brand.Name}}</div>{{end}}
<h1>Security Assessment<span class="sub">{{.Project.Name}}</span></h1>
{{if .Brand.Tagline}}<p class="meta">{{.Brand.Tagline}}</p>{{end}}
<p class="meta">Generated {{date .GeneratedAt}}{{if .Brand.Name}} · Prepared by {{.Brand.Name}}{{end}}</p>
</div>
<h2>Summary</h2>
<p><b>{{.Summary.Total}}</b> finding(s) with supporting evidence across <b>{{.Summary.Applications}}</b> application(s).{{if .Methodology.Summary.Total}} Methodology coverage: <b>{{.Methodology.Summary.CoveredPct}}%</b>.{{end}}</p>
<figure class="chart">{{.SeverityChart}}</figure>
<h2>Findings</h2>
{{if .Findings}}{{range .Findings}}<div class="finding">
<h3><span class="sev sev-{{.Severity}}">{{sevfmt .Severity}}</span> {{.Title}}</h3>
<p class="fmeta">{{.AppName}} · {{.Status}}{{if .CWE}} · {{.CWE}}{{end}}</p>
{{if .Description}}<p>{{.Description}}</p>{{end}}
<ul class="evidence">{{range .Evidence}}<li>{{.Title}}{{if .Location}} — <code>{{.Location}}</code>{{end}}</li>{{end}}</ul>
</div>{{end}}{{else}}<p><em>No reportable findings.</em></p>{{end}}
</body></html>`

const execHTML = `<!doctype html><html><head><meta charset="utf-8">
<title>{{.Project.Name}} — Executive Summary</title>
<style>` + reportCSS + `</style></head><body>
<h1>{{.Project.Name}}<span class="sub">Executive Summary</span></h1>
<p class="meta">Generated {{date .GeneratedAt}}</p>
<p>This engagement covered <b>{{.Summary.Applications}}</b> application(s) and <b>{{.Summary.Assets}}</b>
asset(s), running <b>{{.Summary.TasksRun}}</b> analysis task(s). <b>{{.Summary.Total}}</b> finding(s)
with supporting evidence are reported.</p>
{{if .Methodology.Summary.Total}}<p>Methodology coverage: <b>{{.Methodology.Summary.CoveredPct}}%</b> — {{.Methodology.Summary.Covered}} of {{.Methodology.Summary.Total}} items covered ({{.Methodology.Summary.NotApplicable}} n/a).</p>{{end}}
<h2>Findings by severity</h2>
<figure class="chart">{{.SeverityChart}}</figure>
<table><tr><th>Severity</th><th>Count</th></tr>
{{range sevs}}<tr><td><span class="sev sev-{{.}}">{{sevfmt .}}</span></td><td>{{index $.Summary.BySeverity .}}</td></tr>{{end}}
</table>
<h2>Summary of findings</h2>
{{if .Findings}}<ul class="findlist">{{range .Findings}}<li><span class="sev sev-{{.Severity}}">{{sevfmt .Severity}}</span> {{.Title}}{{if .CWE}} <span class="cwe">{{.CWE}}</span>{{end}} <span class="app">{{.AppName}}</span></li>{{end}}</ul>{{else}}<p><em>No reportable findings.</em></p>{{end}}
</body></html>`

const techHTML = `<!doctype html><html><head><meta charset="utf-8">
<title>{{.Project.Name}} — Technical Report</title>
<style>` + reportCSS + `</style></head><body>
<h1>{{.Project.Name}}<span class="sub">Technical Report</span></h1>
<p class="meta">Generated {{date .GeneratedAt}}</p>
<h2>Scope</h2>
{{if .Scope}}<ul>{{range .Scope}}<li>{{.Kind}}: <code>{{.Value}}</code></li>{{end}}</ul>{{else}}<p><em>No scope allowlist defined (all targets permitted).</em></p>{{end}}
<h2>Coverage</h2>
<ul>
<li>Applications: {{.Summary.Applications}}</li>
<li>Assets: {{.Summary.Assets}}</li>
<li>Tasks run: {{.Summary.TasksRun}}</li>
<li>Capabilities exercised: {{if .Summary.Capabilities}}{{range $i, $c := .Summary.Capabilities}}{{if $i}}, {{end}}<code>{{$c}}</code>{{end}}{{else}}none{{end}}</li>
</ul>
<h2>Findings by severity</h2>
<figure class="chart">{{.SeverityChart}}</figure>
<h2>Remediation coverage</h2>
<figure class="chart">{{.CoverageChart}}</figure>
{{if .Methodology.Summary.Total}}<h2>Methodology coverage</h2>
<p><b>{{.Methodology.Summary.CoveredPct}}%</b> covered ({{.Methodology.Summary.Covered}}/{{.Methodology.Summary.Total}}; {{.Methodology.Summary.NotApplicable}} n/a)</p>
{{range .Methodology.Packs}}<div class="finding"><h3>{{.Title}}</h3><ul class="findlist">{{range .Items}}<li><span class="cwe">{{.Status}}</span> {{.Item.Title}}</li>{{end}}</ul></div>{{end}}{{end}}
<h2>Findings</h2>
{{if .Findings}}{{range .Findings}}<div class="finding">
<h3><span class="sev sev-{{.Severity}}">{{sevfmt .Severity}}</span> {{.Title}}</h3>
<p class="fmeta">Application: <b>{{.AppName}}</b> · Status: {{.Status}}{{if .CWE}} · CWE: {{.CWE}}{{end}}</p>
{{if .Description}}<p>{{.Description}}</p>{{end}}
<p class="evlabel">Evidence</p>
<ul class="evidence">{{range .Evidence}}<li>{{.Title}}{{if .Location}} — <code>{{.Location}}</code>{{end}} <span class="prov">origin: {{.Origin}}, {{.ReviewState}}</span></li>{{end}}</ul>
</div>{{end}}{{else}}<p><em>No reportable findings.</em></p>{{end}}
</body></html>`

const reportCSS = `
body{font:15px/1.55 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#1a1f29;max-width:900px;margin:40px auto;padding:0 24px}
h1{font-size:26px;margin:0 0 4px}h1 .sub{display:block;font-size:15px;font-weight:400;color:#6b7280;margin-top:2px}
h2{font-size:18px;margin-top:32px;border-bottom:1px solid #e5e7eb;padding-bottom:4px}
h3{font-size:16px;margin-top:24px}
.meta{color:#6b7280;font-size:13px}
table{border-collapse:collapse;margin:8px 0}th,td{border:1px solid #e5e7eb;padding:5px 12px;text-align:left}
th{background:#f9fafb;font-size:12px;text-transform:uppercase;letter-spacing:.03em}
code{background:#f3f4f6;padding:1px 5px;border-radius:4px;font-size:13px}
.sev{display:inline-block;font-size:11px;font-weight:700;padding:1px 7px;border-radius:4px;color:#fff;text-transform:uppercase}
.sev-critical{background:#7c1d1d}.sev-high{background:#dc2626}.sev-medium{background:#f59e0b}.sev-low{background:#3b82f6}.sev-info{background:#6b7280}
.findlist{list-style:none;padding:0}.findlist li{padding:4px 0}
.cwe{color:#6b7280;font-size:13px}.app{color:#6b7280;font-size:13px;float:right}
.finding{border:1px solid #e5e7eb;border-radius:8px;padding:12px 16px;margin:16px 0}
.fmeta{color:#6b7280;font-size:13px;margin:2px 0 8px}
.evlabel{font-size:12px;text-transform:uppercase;letter-spacing:.03em;color:#6b7280;margin:8px 0 2px}
.evidence{margin:0}.prov{color:#9ca3af;font-size:12px}
figure.chart{margin:12px 0}
`
