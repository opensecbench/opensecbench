# ADR-0003 — Capability & extension package format

Status: Accepted

## Context

The platform's central bet is extensibility: adding a tool (TruffleHog and beyond), a
methodology, a project template, a playbook, a visualization, or a report template should be
*authoring a package*, not patching core. Built-ins must use the exact same public contracts so
the extension API is dogfooded. Because packages can carry executable capabilities and will
eventually come from a community hub, **trust and safety are designed in from the start**.

## Decision

**A capability is a typed operation.** Its manifest declares:

- `id`, `version` (semver).
- `input` / `output` JSON Schemas — parameters and results are validated at the boundary.
- `permissions` — declared network / filesystem / secret needs.
- `execution` — one of `native-go`, `container:<immutable-digest-ref>`, or `external`.

The Analyst (agent) and the user both invoke capabilities through the same registry; each
invocation is a `task`, approval-gated and audited. Capabilities always execute in a sandboxed
runner (ADR-0004). Declared permissions are the enforced ceiling — a capability cannot exceed
what its manifest states, and the declaration is surfaced to the user at install time.

**An extension package** is a manifest + assets bundling any of: capabilities, methodology packs,
project templates, playbooks, visualizations, report templates, triggers. Packages are:

- **Versioned** and installable from a directory or git repo now (a community hub later).
- **Signed** — a signature over the content digest; installs verify it and pin the digest, making
  an installed package immutable.
- **Attributed** — signatures tie to a publisher key; users choose which publishers/keys to
  trust. Unsigned or untrusted packages install only via an explicit, audited override.

First-party built-ins ship as extension packages under `extensions/`, in the identical format.

**Threat model (community hub, designed for now):** assume malicious actors will try to upload
bad packages. Defenses layer: signature + digest pinning, publisher verification, declared+
enforced permissions, mandatory sandboxing, automated submission scanning, tagging/reputation,
and moderation/takedown. None of these are retrofits — the format carries signatures and
permission declarations from v1.

## Consequences

- The capability registry (`pkg/capability`) and package loader (`pkg/extension`) are core P2
  work; the manifest JSON Schemas are versioned under `docs/spec/` once stable.
- Native-Go capabilities and containerized capabilities share one contract, so a capability's
  implementation language is an internal detail behind the typed interface.
- The agent's tool set *is* the capability registry (optionally MCP-exported for external agents),
  which is what keeps "the LLM uses the same capabilities as the user" true by construction.
- Package format versioning + compatibility checks are required as the schema evolves.
