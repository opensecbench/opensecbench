# ADR-0058 — Two-surface triage: Triage + Findings

Status: Proposed. Consolidate the three triage surfaces — Observations, Investigations, Findings —
into **two**: a single **Triage** surface (the pre-confirmation queue plus in-flight validations) and
**Findings** (the confirmed, reportable deliverable). This is a presentation change: the data model
and the disposition routing of ADR-0028/ADR-0037 are unchanged. Supersedes the three-peer-tab UI
those ADRs implied, not their routing logic.

## Context

The evidence pipeline is linear: a scan produces **observations** (raw, deduped interpreted results);
triage confirms the real ones into **findings** (the deliverable) or opens an **investigation** — a
human-gated vuln-validator agent thread — for the ambiguous ones; a confirmed investigation becomes a
finding. Reports pull only from findings (ADR-0045).

The workbench currently exposes this as **three peer tabs** (Observations, Investigations, Findings).
That presents one pipeline as three destinations, and it reads as confusing even to the maintainer:
a newcomer can't tell where to start, or why a given item lives in one tab rather than another.

There are really only two conceptual lines here, not three:

1. **Confirmed vs not** — Findings (the deliverable) vs everything upstream. This is the real boundary.
2. **A row vs a workstream** — observations and findings are table rows; an investigation is an active
   agent thread. That difference is genuine, but it does not need to be a top-level peer tab.

## Decision

Collapse to two workbench surfaces.

**Triage** — everything before confirmation, over the shared `DataTable` with a segmented control:

- **Queue** — observations awaiting a human call (untriaged + `review`-dispositioned). Row actions
  unchanged: **confirm → Finding**, **investigate → Validating**, **dismiss**.
- **Validating** — the open investigation threads. A row opens the validation thread in the side
  panel; when it resolves, **confirm → Finding** (the item leaves Triage) or **dismiss**.

The segmented control carries live counts (`Queue 42 · Validating 3`) so in-flight agent work stays
visible — investigations are active work, not a passive backlog, and must not become buried.

**Findings** — unchanged: confirmed, reportable vulnerabilities; the source for reports.

**Out of scope / unchanged:**

- The `observations`, `investigations`, and `findings` stores and their transitions.
- Disposition routing (ADR-0028): auto-finding / auto-investigate / review still decide where a fresh
  observation lands. Auto-opened investigations simply appear under Triage → Validating.
- Investigations stay first-class entities and API resources; only their *surfacing* changes.

Navigation drops from three items to two (`Triage`, `Findings`) in the workbench bar. `InvestigationsTab`
folds into the Triage surface as its Validating segment; `ObservationsTab` becomes the Queue segment;
`FindingsTab` is untouched.

## Consequences

- The mental model becomes answerable in one sentence: **it's in Triage until it's confirmed, then it's
  a Finding.** "Where do I look" stops being a question.
- Low blast radius: mostly a frontend nav + surface merge. No schema migration, no routing change, no
  API removal (the investigation endpoints stay; the Triage surface calls the same ones).
- Risk: the Validating segment must stay discoverable — an agent thread mid-run that's hidden behind a
  segment toggle is worse than a dedicated tab. Mitigated by the persistent count/badge on the control
  and by surfacing "needs your input" validations in the Overview's *Waiting on you*.
- Reversible: because entities and endpoints are unchanged, re-splitting into separate surfaces later is
  a pure UI change if this proves wrong.
