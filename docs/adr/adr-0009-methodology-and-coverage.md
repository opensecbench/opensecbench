# ADR-0009 — Methodology & coverage

Status: Accepted (built-in packs, per-project adoption + item status, coverage roll-up; evidence↔item
linking via `coverage_observations`, migration 0021, see ADR-0015 Phase 3b); applicability automation and
packs-as-extensions staged

## Context

Methodology is the platform's home base (ADR-0001, the plan): the operator works a technology-specific
checklist — Web, REST, GraphQL, OIDC/OAuth, SAML, Cloud — and **coverage climbs** as items are worked
(by hand, via capabilities, or via playbooks). Until now "coverage" was only a count of tasks and
capabilities run, which says nothing about *what was actually assessed*. We need a real methodology
model with per-item completion so coverage is meaningful in the Workbench and in reports, and so the
methodology-driven operator loop the product centers on exists.

## Decision

Split a **static catalog** from **dynamic per-project state**, mirroring how capabilities (a code
registry) are separate from tasks (DB rows).

### Catalog — `pkg/methodology`

A `Methodology` is a versioned pack of checklist items for a technology/domain; a `Item` is one check:

```
Methodology{ ID, Title, Tech, Version, Items []Item }
Item{ ID, Title, Objective, Procedure, Standards []string, SuggestedCapabilities []string }
```

Item IDs are pack-scoped (e.g. `web-app/idor`). Built-in packs ship first-party in the **same shape an
extension pack will provide** (ADR-0003 dogfooding); the catalog is read-only reference data exposed
through a `Registry` like the capability registry. Standards carry OWASP ASVS / CWE references so a
compliance report can map to them; `SuggestedCapabilities` link a check to the tools that help perform
it (e.g. `web-app/secrets` → `trufflehog`).

### Per-project state — adoption + coverage

A project **adopts** the packs relevant to it (`project_methodologies`), which sets the checklist —
and therefore the coverage denominator. Each item then carries a **coverage status** per project
(`methodology_coverage`):

```
status ∈ { not_started (default, no row) | in_progress | covered | not_applicable }
+ note, updated_at
```

Adoption is explicit now (and will be pre-filled by project templates and, later, by KB-driven
applicability — "target uses SAML → suggest the SAML pack"). A missing coverage row means
`not_started`, so adopting a pack costs nothing until items are touched.

### Coverage roll-up

Coverage is computed over **adopted** items: `applicable = total − not_applicable`,
`covered_pct = covered / applicable`. It is surfaced on the Workbench (the methodology surface) and
replaces the thin task-count coverage in reports. An item may reference the findings/tasks that
evidence its coverage (a link table) — deferred; the status + note is the first-class signal now.

## Consequences

- Coverage becomes a statement about *what was assessed*, not *how many tools ran* — usable in exec
  and technical reports and as the project's progress indicator.
- Catalog/state separation keeps methodology content versionable and shareable (packs) without
  entangling it with engagement data, and lets the same pack drive many projects.
- Adoption gives a correct denominator per engagement (a web project isn't judged against Cloud items).
- The `SuggestedCapabilities` link is the seam for "run this check" — a later slice can launch a
  capability/playbook straight from a methodology item and auto-advance its status.

## Alternatives considered

- **Methodology items as DB rows (fully dynamic)**: rejected — checklists are reference content that
  should version and ship as packs, like capabilities; per-project mutation belongs only to status.
- **No adoption, measure against the whole catalog**: rejected — the denominator would be meaningless
  (every project scored against every technology).
- **Track coverage only implicitly from findings/tasks**: rejected — "not applicable" and
  "checked, nothing found" are essential states that tool activity can't express; the operator must be
  able to assert coverage explicitly.
