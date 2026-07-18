# ADR-0002 — Data model & provenance

Status: Accepted

## Context

Evidence lineage is the product's differentiator. A finding must be traceable to the raw evidence
supporting it, and AI-generated conclusions must be distinguishable from confirmed evidence. The
model must also support durable, cross-engagement knowledge (a target assessed repeatedly) and a
flexible org/group hierarchy for work use.

## Decision

SQLite for structured data; content-addressed storage (CAS) for immutable artifacts. Provenance
is enforced by explicit foreign keys plus `origin` and `review_state` enums.

**Durability boundary.** The `target` (a real-world system) is durable and survives across
engagements; a `project` is a time-boxed engagement referencing one or more targets. The
knowledge base and prior coverage hang off the `target`, so re-assessment starts ahead.

**Core entities** (see the plan for the full list):

- Hierarchy: `organization → group → project → application → asset`; durable `target` referenced
  by projects. Assets carry a `type` and a `sensitivity` (`open_source | private`).
- Execution: `capability` (versioned) · `tool_adapter` · `extension` · `runner` (bindable to a
  project) · `task` (with `actor`: human | thread) · `playbook`/`playbook_run` · `session`.
- Evidence chain: `artifact` (immutable, CAS) → `observation` (`origin`: tool | thread | human;
  `review_state`) → `evidence` (a whole artifact **or a selection within one**) → `finding`
  (`supported_by` evidence).
- Knowledge: `kb_entry` (scoped: target | group | org | global-personal; provenance + review).
- Governance/audit: `policy_profile`, `provider`, `agent_budget`, `secret`, `canary`,
  `dlp_event`, `approval`, `audit_event` (append-only, hash-chainable).

**The provenance chain is the spine:**

```
finding → evidence (artifact ± region) / observations
        → (artifact + llm_interaction/thread) → task
        → (capability+version, tool+version, runner)
```

**Provenance rules:**

- Artifacts are immutable and content-addressed (sha256); nothing mutates a stored artifact.
- Every `observation` records its `origin`; agent- and tool-derived observations start
  `unreviewed` and only a human transition to `confirmed` lets them support a finding.
- `evidence` may reference a fragment of an artifact (text offset range, image region, transcript
  line range, a specific request/response) so a highlighted excerpt is first-class and traceable.
- Threads (the Analyst's conversations) are provenance actors alongside human and tool.

## Consequences

- Schema evolves via ordered migrations under `migrations/`; the format is versioned and
  compatibility is checked at startup.
- CAS growth (pcaps, screenshots, session recordings) requires a retention/GC policy and
  per-project quotas — tracked for a later phase, but the immutability/addressing model is fixed
  now.
- `review_state`/`origin` are not optional metadata; they are enforced at the boundary where a
  finding is assembled, so AI output cannot become evidence by omission.
