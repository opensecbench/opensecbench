# ADR-0045 — `generate_report` agent tool

Status: Accepted — delivered. The Analyst can compile the project's confirmed findings into a durable,
downloadable **report deliverable** via a `generate_report` tool — the artifact an assessment hands back.
Completes ADR-0035's remaining "agent report" item (the last is parallel steps / deeper delegation).

## Context

The Analyst could find, validate, and propose issues, and a human could confirm them into findings — but
producing the actual report deliverable required a person to call the report API by hand (or click through
the UI). There was no way for the agent to close the loop and hand back the document. The report machinery
(ADR-0008: builder + templates + CAS storage) already existed; it just wasn't reachable as a tool.

## Decision

**A `generate_report` tool** (`pkg/analyst/reporttool.go`). It reuses the report **builder** (ADR-0008):
`report.NewBuilder(store).Build(project)` snapshots the project's evidence-backed findings, a built-in
template renders it, and the bytes are stored in the CAS as an artifact with a `report` record — exactly the
path the HTTP endpoint uses. It returns the report id, artifact id, template, format, and finding count.

- **Evidence-bound, not generative.** The report is built from confirmed findings only (which themselves
  require confirmed observations, ADR-0005). The agent picks the template and audience; it cannot inject or
  invent content — so an autonomous run can't fabricate a deliverable.
- **`md` default, `html` optional.** Markdown needs no headless browser, so it works in any autonomous run;
  HTML is available. PDF/DOCX (browser / heavier renderers) stay on the human HTTP path.
- **Params:** `template` (technical | executive | compliance | retest; default technical), `format`
  (md | html; default md).

**Where it lives.** Added to the `report-writer` and `pentester` profiles — the roles that operate **after**
findings are confirmed. It is deliberately **not** in the autonomous `assessment` playbook's report step:
that step runs propose-only (the `assessor`, no `create_finding`), so no findings are confirmed yet — it
drafts to the workspace, and a human confirms the proposed findings before the deliverable is compiled.
So the flow is: autonomous assess → **human confirms findings** → `generate_report` (report-writer, or a
human ask) produces the document.

## Consequences

- **The loop closes.** find → validate → propose → *human confirms* → **report** is now fully expressible by
  the Analyst; the deliverable is a first-class tool output, stored and downloadable like any report.
- **No new trust surface.** `generate_report` is local (no egress) and reads only confirmed findings, so it
  needs no approval gate — it can't leak or fabricate. It reuses audited storage (artifact + report records).
- **One report path.** By reusing the builder/templates/CAS, an agent-generated report is identical to a
  UI/CLI-generated one — same evidence rules, same formats, same storage.

## Out of scope — later
PDF/DOCX from the agent (browser dependency — stays on the HTTP path); a report **preview** tool (render
without storing); letting the agent attach a workspace-drafted narrative (executive prose) on top of the
builder's structured content; emailing/exporting the report to an integration. Parallel plan steps + deeper
delegation is the remaining ADR-0035 autonomy item.

Composes with ADR-0008 (the report builder/templates this exposes), ADR-0005 (the confirmed-evidence rule it
inherits), and ADR-0035/0044 (the autonomous assessment + approval gate it produces the deliverable for).
