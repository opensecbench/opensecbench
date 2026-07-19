# ADR-0040 — Knowledge capture loop

Status: Accepted — delivered. Turn what an engagement *discovers* about a target into *durable* knowledge:
a `knowledge-scribe` agent distills the analysis, observations/findings, and corpus into knowledge-base
drafts (architecture, auth, tech_stack, environment, data_flow, convention, gotcha), a `list_kb` tool lets
it see existing knowledge to avoid duplicates, and a capture step is wired into the playbooks. Drafts stay
human-confirmed and carry across engagements on the same target.

## Context

OSB had a solid durable knowledge primitive — the target-anchored KB (provenance, review discipline,
automatic per-target carry-over across engagements, kinds that mirror how a system is set up) — but almost
nothing fed it automatically. The systematic discovery paths (scanners → observations/findings; the
onboarding/recon/assessment playbooks) deposited their output in **ephemeral workspace markdown, reports, and
the observation queue**, never distilling into the KB. Only the tech-scout (ADR-0038) deliberately drafted
KB, and only for the stack. So discoveries about how an org/team is set up evaporated into reports. This
closes that loop (chosen first of several knowledge investments; org/group scope, a dossier view, and
freshness are follow-ons).

## Decision

**`list_kb` tool.** Returns the current project's target-anchored KB entries (id, target, kind, title,
review_state), filterable by kind — in the shared `reads` bundle so any profile can see what's known. The
agent previously could only reach KB by keyword `search`, semantic `search_corpus`, or a known id; it could
not enumerate a target's knowledge to update rather than duplicate.

**`knowledge-scribe` profile.** A least-privilege distiller — reads (incl. `list_kb`, `list_observations`,
`list_findings`, `search_corpus`, `read_context`, `workspace_read`) + `draft_kb_entry`, and **nothing that
mutates state or reaches out** (no `create_finding`, scans, `send_request`, `web_fetch`, `run_code`). Its
persona: review the analysis notes, observations/findings, corpus, and existing KB, then distill the
**durable** facts about how the target is set up into one KB draft per distinct fact, the right kind,
anchored to the target — updating rather than duplicating, and capturing stable how-it-works knowledge, not
transient vulnerabilities.

**Capture wired into the flow.** A standalone `capture-knowledge` playbook (run any time to compile current
knowledge), and a `capture` step added to the `onboarding` playbook so the first-engagement flow builds
durable knowledge instead of just a kickoff report.

## Consequences

- **Discoveries become durable, reusable knowledge.** The stack, architecture, auth model, data flows, and
  conventions a run uncovers are captured once, confirmed by a human, and inherited by future engagements on
  the same target — the KB stops being a filing cabinet nothing files into.
- **Human-confirmed by construction.** Scribe drafts are `unreviewed`; a person confirms/rejects. This is the
  backstop against a hallucinating model (see below) — no durable knowledge lands without human sign-off.
- **Least privilege.** The scribe only reads and drafts; it cannot scan, create findings, or send traffic.

## Real-tool note (2026-07-19)
Live-validated the wiring (profile registered, tools offered) but the end-to-end run via the **claude-cli**
provider was inconclusive: on an open-ended multi-tool task it exhibited the known prompted-tool-use
unreliability — it *fabricated* a tool result instead of yielding to the loop, and drafted nothing. This is
a provider limitation (ADR-0017 native tool-use, on by default for API providers, is the reliable path), not
a capture-loop defect; the loop is validated by unit tests, and the human-confirm gate catches any
hallucinated draft regardless of provider.

## Out of scope — later (the other knowledge investments)
- **Org/team-level knowledge**: make KB scope above `target` real (group/org anchoring + an inheritance walk)
  so shared auth/infra/conventions persist once and apply across a team's apps (today it's per-target only).
- A synthesized **dossier** ("everything we know about this org/target"); **freshness** (last-verified /
  staleness so knowledge doesn't silently rot); a `derived` KB origin for distilled entries; project→target
  RAG carry-over without a manual reindex; deterministic promotion of specific findings.

Composes with ADR-0010 (KB), ADR-0038 (the tech-scout, whose stack focus this generalizes to the whole
setup), ADR-0039 (drafts are RAG-indexed on write), and ADR-0019/0035 (profiles, playbooks).
