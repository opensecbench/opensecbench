# OpenSecBench design docs

Architecture Decision Records (ADRs) and open-format specifications. Per our working agreement,
**a short design doc lands here before its subsystem is built**, and docs are kept current as
decisions change. The authoritative product/roadmap plan lives outside the repo (planning
scratch); these docs are the buildable, versioned foundation.

## ADRs

| # | Title | Status |
|---|-------|--------|
| [0001](adr-0001-architecture-overview.md) | Architecture overview & boundaries | Accepted |
| [0002](adr-0002-data-model-and-provenance.md) | Data model & provenance | Accepted |
| [0003](adr-0003-capability-and-extension-format.md) | Capability & extension package format | Accepted |
| 0004 | Runner protocol & sandboxing | Planned (before P2) |
| 0005 | Agent runtime & provider layer | Planned (before P4) |
| 0006 | Policy, DLP & audit | Planned (before P4) |

Later subsystems (proxy/session, KB, reporting/viz, collaboration) get an ADR when their phase
begins. See [`../TODO.md`](../TODO.md).

## Conventions

- One ADR per decision; format: **Context → Decision → Consequences**.
- ADRs are append-only in spirit: supersede rather than silently rewrite; note status changes.
- Format specs (capability manifest, extension package, playbook, project template) are versioned
  JSON Schemas under `spec/` once they stabilize; ADR-0003 defines their shape.
