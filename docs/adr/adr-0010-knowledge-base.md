# ADR-0010 — Knowledge base

Status: Accepted (target-anchored entries, project inheritance, agent drafting, review, search);
group/org/personal scope resolution, versioning, and KB-driven applicability staged

## Context

Re-assessing a system should start far ahead of the first time. The plan makes a knowledge base
first-class: capture **how a target is built** — architecture, auth model, endpoints, tech stack,
environments, data flows, conventions, hard-won gotchas — so the *next* engagement inherits it. KB
must be **anchored to the durable `target`, not the time-boxed `project`** (engagements come and go;
the target persists), auto-drafted by the Analyst yet human-curated, provenance-bearing, and governed
by the same sensitivity/egress rules as everything else (ADR-0005, ADR-0006).

## Decision

### Entries anchor to a target; projects inherit

A `kb_entry` belongs to a **`target`**. A project references one or more targets (existing
`project_targets`), so a project's KB view is the union of entries across the targets it references —
a new engagement against a known target inherits its accumulated knowledge automatically.

```
KBEntry{
  ID, TargetID,
  Kind,        // architecture | auth | endpoint | tech_stack | environment | data_flow |
               //   convention | gotcha | tactic
  Scope,       // target (now) | group | org | global  (broader scopes staged)
  Title, Body, Tags,
  Sensitivity, // open_source | private  (reuses the asset enum)
  Origin,      // human | thread (agent-drafted) | derived
  ReviewState, // unreviewed | confirmed | rejected  (reuses ADR-0005)
  SourceRef,   // provenance hint: the task/thread/finding it was derived from
  CreatedAt, UpdatedAt
}
```

Kind, Sensitivity, Origin, and ReviewState reuse existing enums so KB rides the same governance and
review machinery as observations rather than inventing parallel concepts.

### Provenance-first, like evidence

The Analyst drafts entries through a `draft_kb_entry` tool: entries land **`origin = thread`,
`review_state = unreviewed`**, clearly marked as AI-authored, and never authoritative until a human
confirms — exactly the observation → finding discipline (ADR-0005). Drafting only *writes an
unreviewed note* (it doesn't touch a target), so it is ungated but audited; humans confirm, edit, or
reject. Human-authored entries default to `confirmed`.

### Governed by sensitivity

Entries are sensitivity-classified. A private target's KB is treated like any private data: the
agent-egress policy already refuses to send private content to a public provider, so drafting/reading
KB for a private target stays on local/approved providers. AI-authored entries are visibly labelled.

### Feeds context, search, and (later) methodology

KB is surfaced as a **Knowledge** section in the Workbench and is included in **omni-search**. It
feeds the Analyst's context and, in a later slice, drives **methodology applicability** — e.g. an
`auth` entry noting SAML suggests adopting the SAML pack (ADR-0009). The `tactic` kind lets
playbook/methodology improvements accumulate as reusable knowledge.

## Consequences

- Knowledge outlives engagements and compounds: the durable-target anchor is what makes "start ahead
  next time" real, and it reuses the `project_targets` link already in the schema.
- Reusing the sensitivity/origin/review enums means KB inherits egress governance and the
  human-in-control review gate for free; AI drafts can't silently become trusted knowledge.
- Anchoring to a target now (not group/org/global) keeps the first slice correct and simple; broader
  scopes are additive (a `scope` column is present for forward-compatibility).

## Alternatives considered

- **Anchor KB to the project**: rejected — it would die with the engagement, defeating the whole
  point (inheritance across re-assessments).
- **A separate KB review workflow**: rejected — observations already model unreviewed→confirmed with
  provenance; KB reuses it instead of duplicating.
- **Free-form notes only**: rejected — typed `kind`s make KB queryable and let it drive methodology
  applicability and context retrieval; a plain note blob couldn't.
