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
| [0004](adr-0004-runner-protocol.md) | Runner protocol & sandboxing | Accepted |
| [0005](adr-0005-evidence-interpretation.md) | Evidence interpretation & finding lifecycle | Accepted |
| [0006](adr-0006-agent-runtime-and-providers.md) | Agent runtime & provider layer | Accepted |
| [0007](adr-0007-http-repeater-and-sessions.md) | HTTP capture, Repeater & interactive sessions | Accepted (Repeater + Terminal + Proxy); SSH & agent co-drive staged |
| [0008](adr-0008-reporting-and-visualization.md) | Reporting & visualization | Accepted (exec+technical, MD/HTML/PDF, SVG); more types staged |
| [0009](adr-0009-methodology-and-coverage.md) | Methodology & coverage | Accepted (packs + adoption + coverage); applicability automation staged |
| [0010](adr-0010-knowledge-base.md) | Knowledge base | Accepted (target-anchored, inheritance, drafting, search); broader scope staged |
| [0011](adr-0011-secrets-dlp-redaction.md) | Secrets vault, DLP & redaction | Accepted (vault, injection, redaction, DLP+canaries); KMS staged |
| [0012](adr-0012-collaboration-export-import.md) | Collaboration: portable export/import | Accepted (encrypted bundle); mediated sharing + signing staged |
| [0013](adr-0013-extension-loader.md) | Extension loader | Accepted (dir packages, container caps + methodology, ed25519 signing); more pack types staged |
| [0014](adr-0014-community-hub.md) | Community extension hub | Accepted (static signed index, publish/browse/install, explicit trust); scanning/reputation staged |
| [0015](adr-0015-workbench-ide-shell.md) | Workbench IDE shell | Accepted (Phase 1 — frame reshape); explorer, methodology-landing, persistent document tabs staged |

Later subsystems get an ADR when their phase begins. See [`../TODO.md`](../TODO.md).

## Conventions

- One ADR per decision; format: **Context → Decision → Consequences**.
- ADRs are append-only in spirit: supersede rather than silently rewrite; note status changes.
- Format specs (capability manifest, extension package, playbook, project template) are versioned
  JSON Schemas under `spec/` once they stabilize; ADR-0003 defines their shape.
