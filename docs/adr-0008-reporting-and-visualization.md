# ADR-0008 — Reporting & visualization

Status: Accepted (executive + technical reports, MD/HTML, inline-SVG figures); PDF, more types,
and interactive views staged

## Context

An engagement's deliverable is a report, and different audiences need different ones — an executive
summary, a detailed technical report, a retest, a compliance mapping, a client-branded document
(the plan, P8). Reports must bind real project data (findings, their evidence + provenance,
coverage, scope) and only present **confirmed findings with traceable evidence** — AI conclusions
never silently become report content (ADR-0005). Reports and their embedded figures must be
portable and self-contained (no external network at view time), mirroring the artifact constraint.

## Decision

### Report data is gathered, not queried ad hoc

`pkg/report` defines a `Data` snapshot — project metadata, scope, a coverage summary (applications,
assets, tasks run, capabilities exercised), and the reportable findings, each expanded with its
supporting **evidence observations** (title, severity, location, provenance to an artifact). A
`Builder` assembles `Data` from a narrow `Source` interface (satisfied by `*store.DB`), so gathering
is testable with a fake and the render path is pure.

**Reportable = has traceable evidence and is not a false positive.** A finding with no supporting
observations, or status `false_positive`, is excluded. This encodes the provenance-first rule at
the report boundary.

### Templates render Data to a format

A `Template` is `{ID, Title, Kind, render(Data, Format) → bytes}`. Built-ins ship first-party in
the **same shape an extension package will provide** (dogfooding, per ADR-0003): `executive`
(summary + severity chart) and `technical` (per-finding detail + evidence). `retest`, `compliance`,
and `branded` follow. Rendering uses Go `text/template` (Markdown) and `html/template` (HTML, with
contextual escaping — finding text is untrusted).

Formats: **Markdown and HTML now**; **PDF** by rendering the HTML through a **headless Chromium**
(`--headless --print-to-pdf`), reusing the browser detection built for the proxy — optional, and a
missing browser degrades to "MD/HTML only" rather than failing. **DOCX** is deferred (P12).

### Visualizations are self-contained inline SVG

`pkg/viz` turns aggregates into **inline SVG** figures — no JavaScript, no external assets — so they
embed directly in HTML/PDF reports and render anywhere (this also satisfies the Artifact CSP for a
future in-app preview). First figures: a **severity distribution** bar and a **coverage** summary.
Richer, *interactive* views (pan/zoom topology, dependency graphs) come later as workbench tabs;
the static-SVG figure is the portable report-embeddable form and the common denominator.

Figures are keyed to data shapes, not tools, so any capability output that produces the shape can be
visualized. Packaging viz as extensions follows once the built-in set stabilizes.

### Generated reports are provenance-bearing artifacts

A generated report is written to the **CAS** and recorded as a `report` row (template, format,
project, created_at, artifact) so it is immutable, auditable (audit action `report.generate`), and
re-downloadable — the same provenance discipline as tool artifacts.

## Consequences

- The "confirmed evidence only" rule lives in one place (the `Builder`), so every template inherits
  it — no template can leak an unreviewed observation into a deliverable.
- Pure render + `Source` interface makes report content unit-testable without a live database or a
  browser; PDF is the only path needing external tooling and it is optional.
- Inline-SVG figures keep reports single-file and viewable offline, and the same figures preview in
  the workbench and embed in reports without a second rendering path.
- Reusing the proxy's browser detection for PDF avoids a new heavyweight dependency
  (wkhtmltopdf/gofpdf) while giving real HTML fidelity.

## Alternatives considered

- **A Go PDF library (gofpdf/maroto)**: rejected as the primary path — manual layout diverges from
  the HTML template and doubles the templating work. Headless-Chrome print reuses one HTML source.
- **Client-side JS charting (Chart.js/D3) in HTML reports**: rejected — violates self-contained/
  offline and the Artifact CSP. Server-rendered inline SVG needs no runtime.
- **Rendering straight from live queries per template**: rejected — every template would re-encode
  the evidence-traceability rule and re-run the same joins; the gathered `Data` snapshot centralizes
  both and is what a report artifact should immortalize.
