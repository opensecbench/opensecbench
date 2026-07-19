# ADR-0035 — Signal-aware autonomous assessment

Status: Accepted — delivered. The Analyst can drive a full source assessment end-to-end (recon → scan →
signal-aware triage → validation → draft report) as a bounded background run that **proposes** issues for
human confirmation. It triages by the reachability/exposure/route signals (ADR-0029–0034) instead of blind
severity, and the investigation agent is now seeded with those signals.

## Context

The autonomy *machinery* was already delivered (ADR-0019/0020): least-privilege profiles, a DAG playbook
runner, delegation, scheduling, and a governed toolset that can run capabilities, read all evidence, build
PoCs, and propose findings. Three gaps kept it from driving a real assessment and from using the
reachability work:

1. **The agent was blind to the signals.** The investigation seed dropped `obs.Attributes`, and no tool
   returned observations with their `reachable`/`exposed`/`exposed_route`/`dataflow_source` attributes — so
   no step could prioritize by them.
2. **No end-to-end assessment** — the built-in playbooks were thin (onboarding/recon/triage-report).
3. **A governance seam** — a running plan auto-authorizes every tool, so an autonomous run never pauses.

James chose **propose, human confirms**: the autonomous run gathers evidence and proposes, but must not
auto-confirm findings — leaving confirmation to the existing review UI, without building a pausable plan
runner.

## Decision

**Two read tools expose the triage queue** (`list_observations`, `list_investigations`, in the shared
`reads` bundle so every profile has them). `list_observations` returns each observation with its full
`attributes` map, so a step can rank by reachability + exposure + route + severity. Read-only → ungated.

**The investigation seed carries the signals.** `describeSignals(obs.Attributes)` renders a compact block —
reachability (+ dataflow source), exposed route (traffic-confirmed vs declared), CVSS, verified secret,
package/fix — appended to the vuln-validator seed so it starts grounded in *why* this was flagged.

**An `assessor` profile — the propose-mode worker.** Reads + `run_capability`, `send_request`, `run_code`,
`workspace_*`, `create_observation` — and deliberately **no `create_finding` / `set_coverage`**. Findings
stay human-confirmed by *construction*: a tool not in the profile is never offered to the model.

**An `assessment` playbook** chains create_finding-less steps: `recon` (code-analysis: route-map) → `scan`
(code-analysis: semgrep/grype/govulncheck/trufflehog) → `triage` (assessor: rank the queue,
reachable-on-exposed-route first) → `validate` (assessor: evidence/PoC, record observations, propose) →
`report` (assessor: draft `reports/assessment.md`). Triggerable interactively or on a schedule; the human
then confirms findings and renders the deliverable via the existing review UI + reports API.

## Consequences

- **The AI drives the assessment, signal-first.** Reachable, exposed issues rise to the top of triage and
  validation automatically — the reachability/exposure work becomes actionable, not invisible.
- **Findings stay human-gated, structurally.** No agent step on the autonomous path can create a finding;
  "propose, human confirms" is enforced by the toolset, not by trust. (The deterministic disposition layer's
  high-confidence auto-findings — e.g. verified secrets, ADR-0028 — are unchanged; that is a pre-approved
  mechanism, not the agent acting.)
- **report-writer stays as-is.** It intentionally *can* create findings (a report agent formalizing a
  validated one), so the autonomous assessment uses `assessor` for its draft step rather than weakening an
  existing profile.
- **Grounded investigations.** A human clicking "Investigate" now hands the agent the reachability/route
  context, not a bare rule id.

## Out of scope — later
Mid-run plan approval (a pausable/resumable runner — the governance option not chosen here); an agent
`generate_report` tool (the report step drafts prose; final render stays the human's reports API); parallel
plan steps, deeper delegation, and raising the 8-step sub-agent cap; triage-state context injection beyond
the pull tools.

Composes with ADR-0019/0020 (profiles, runner, toolset), ADR-0028 (investigations), and ADR-0029–0034 (the
signals it triages on).
