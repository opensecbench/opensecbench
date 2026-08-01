# ADR-0060 — Full-fidelity project bundle (demo & backup mode)

Status: Proposed

## Context

ADR-0012 defined the portable project bundle as a **shareable deliverable**: project, targets,
applications, assets, scope, findings, their supporting observations, evidence artifacts + CAS blobs,
and the KB. It **deliberately excluded** engagement-internal or regenerable data — proxy exchanges,
tasks, audit, sessions, Analyst threads, reports — on the reasoning that a bundle handed to a client
should be the findings package, and reports regenerate from findings.

That subset is right for its purpose, but it does not serve two other real needs:

- **Demos.** To show the tool working, someone should be able to load a shared project and see it as
  if live — the Analyst dock populated with its conversation, the triage/investigations workflow, the
  captured HTTP traffic, generated reports, methodology coverage — *without* running a live engagement.
  Today a loaded bundle leaves the AI dock, Proxy, Investigations, and Reports surfaces empty.
- **Backup / migration.** Moving a working project between machines or instances with its full state,
  not just its deliverable.

Both need the *working state*, which is exactly what ADR-0012 leaves out.

## Decision

Add a **full mode** to `pkg/bundle` alongside the existing shareable mode. `Export` takes a `full`
flag; the default (shareable) bundle is unchanged from ADR-0012. Full mode additionally captures:

- **Analyst threads + messages** — the AI conversation history.
- **Investigations** — the triage/validation threads (remapped onto their observations).
- **HTTP exchanges** — captured proxy/replay traffic.
- **Reports** — generated deliverables + their CAS-rendered blobs.
- **Context / input artifacts** — uploaded docs, notes, emails (input-origin artifacts not tied to a
  finding's observation), + their CAS blobs.
- **Methodology adoption + coverage** — adopted packs and per-item status.
- **Engagement record** — scope authorization, contacts, test accounts.

`bundle.Data` gains a slice per entity and `FormatVersion` bumps **1 → 2**. Import recreates the new
entities through the normal store constructors, extending the existing old→new ID maps and rewriting
foreign keys in dependency order:

```
targets → project → applications → assets → blobs → artifacts → observations → findings → KB → scope
        → threads → messages(threadMap) → investigations(obsMap)
        → exchanges → reports(+blobs) → context artifacts(+blobs) → methodology → engagement
```

New CAS blobs (report renders, context documents) travel like evidence blobs — base64-embedded in the
bundle keyed by sha256, re-entering the destination CAS under the *same* hash, so content-addressed
provenance survives the trip. Encryption (AES-256-GCM + scrypt) and publisher-key signing from
ADR-0012 apply identically.

### Full mode is not client-facing

A full bundle carries the Analyst's raw reasoning and complete captured traffic. It is for **demos,
backup, and intra-team migration** — not a client deliverable. The default shareable bundle is
unchanged and remains the client-facing path. The CLI/API make full mode explicit (`--full`) so it is
never produced by accident.

## Consequences

- A loaded full bundle renders every surface (AI dock, Proxy, Investigations, Reports, coverage) as if
  the project were live — the demo goal.
- **Version bump.** A pre-2 daemon rejects any v2 bundle (import already guards
  `Version > FormatVersion`); a v2 daemon still reads v1 bundles (the new slices are simply empty).
- Bundle size grows with full mode (traffic + report renders + Analyst transcripts). Acceptable for
  the demo/backup use case; the shareable mode stays lean.
- Supersedes ADR-0012's "reports/exchanges/threads deliberately excluded" stance *for full mode only*;
  the exclusion still holds for the default shareable bundle.
