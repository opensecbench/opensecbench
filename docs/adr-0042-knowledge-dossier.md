# ADR-0042 — Knowledge dossier

Status: Accepted — delivered. A consolidated "what we know about this system" view — the project's/target's
inherited knowledge-base entries grouped by kind into a read-first brief (architecture, tech stack, auth,
environment, data flows, endpoints, conventions, gotchas, tactics). The thing an assessor — or the agent —
reads first to orient before assessing.

## Context

The KB was a flat, per-entry list; the only way to see what was known was `get_kb_entry` (by id), keyword
`search`, or semantic `search_corpus`. With ADR-0040 (capture loop) filling the KB and ADR-0041 (org scope)
letting knowledge inherit across a team, there was now real substance to consolidate — but no assembled view
of it. This is the third of four knowledge investments (freshness is the last).

## Decision

**Deterministic assembly (`pkg/dossier`).** `Assemble(subject, []KBEntry) → Dossier`: groups the entries
(already inheritance-resolved by ADR-0041's `ListKBBy*`) by kind, in a fixed **reading order** — the big
picture first (architecture, tech_stack), then how it's secured and deployed (auth, environment, data_flow,
endpoint), ending with what to watch for (conventions, gotchas, tactics). Within a kind: confirmed before
drafts, most-specific scope first. Rejected entries are dropped. A `Markdown()` render produces the brief,
marking inherited (org/group/global) knowledge and unconfirmed drafts. Pure and deterministic — **no model
call**, so it's always available and free.

**Surfaces.** A `get_dossier` agent tool (in the `reads` bundle) so the agent reads the consolidated view
first instead of enumerating raw entries; `GET /v1/targets/{id}/dossier` and `GET /v1/projects/{id}/dossier`
(JSON, or `?format=markdown`); and `osb dossier --target|--project`.

## Consequences

- **Orientation in one read.** Instead of piecing together scattered KB entries, an assessor (or the agent
  at the start of a run) gets the whole picture of how the target is set up, organized the way you'd read it,
  with inherited org knowledge folded in and drafts flagged.
- **Free and always on.** Deterministic assembly means no provider dependency — the dossier works with any
  (or no) LLM configured, unlike a synthesized narrative.
- **Inheritance made visible.** Org-level facts (a shared auth provider, org conventions) appear in every
  app's dossier, tagged with their scope, so the reader sees what's team-wide vs system-specific.

## Out of scope — later
An optional **LLM-narrative** executive summary layered on top of the deterministic structure (richer prose,
but needs a provider and costs tokens); a rendered dossier surface in the workbench UI; including recent
findings/observations as a "current posture" section; **freshness** (the last knowledge investment —
last-verified/staleness so the dossier flags possibly-stale knowledge).

Composes with ADR-0010 (the KB), ADR-0040 (the capture loop that fills it), and ADR-0041 (the inheritance
this assembles).
