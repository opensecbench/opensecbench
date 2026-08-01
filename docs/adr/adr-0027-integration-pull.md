# ADR-0027 — Persisted integration configs + inbound pull

Status: Accepted — delivered. Integrations gain **reusable per-project configs** (used by both push and
pull) and an **inbound pull** that imports external findings as observations into triage — making
OpenSecBench a two-way hub rather than a push-only sink.

## Context

Integrations were push-only (`Connector.PushFinding`; Jira + DefectDojo), and every push re-sent its
connection details (base URL, project key, credential) in the request body — nothing was persisted. This
increment stores config once per (project, integration), and adds pull for DefectDojo. Confirmed
decisions: config is **per-project**; pulled findings become **observations** that enter the normal triage
flow (nothing external silently becomes a finding).

Two model constraints shaped it: observations carried **no `project_id`** (they scope via a task), and
there was **no external-id dedup** for inbound.

## Decision

**Persisted config** (`integration_configs`, unique per `(project_id, integration)`): base URL, project
key, and a `credential` that is a **vault secret name** (never a value). Push now resolves the stored
config for a finding's project when the body omits it (backward compatible with an explicit `base_url`
override); pull uses it directly. Credential resolution reuses the push path (`GetSealed` + `vault.Open`).

**Inbound pull.** A connector may implement an optional `Puller` (`Pull(ctx, cfg) []ExternalFinding`);
push-only connectors don't, and the pull endpoint 400s for them. DefectDojo's `Pull` lists
`/api/v2/findings/?test={key}` with `Authorization: Token <cred>`, normalizing each to an `ExternalFinding`
(severity lowercased, `verified`→`Confirmed`). `POST /v1/projects/{id}/integrations/{integration}/pull`
maps each new external finding to an **observation** (`origin=tool`, project-scoped, `RuleID
="<integration>:<external_id>"`, `Location=url`, review = confirmed if verified else unreviewed), skipping
any already imported. Returns `{imported, skipped, total}`.

**Two model changes.** `observations.project_id` (nullable) gives a task-less, project-scoped evidence
path — used by integration pull (and it fixes analyst thread-origin observations too);
`ListObservationsByProject` now unions direct + task-scoped. `integration_imports` (unique
`(project_id, integration, external_id)`) provides inbound idempotency so a re-pull imports only new
findings. Imports reuse `origin="tool"` (the `origin` CHECK allows only `tool/thread/human`; adding a value
would need a table rebuild), with provenance in `rule_id`/`location`.

## Consequences

- **Two-way tracker sync.** Configure DefectDojo once; push findings out and pull findings in. Pulled
  findings land as observations in the existing "to triage" flow (they flow into the Home cockpit count
  automatically via `ListObservationsByProject`).
- **No re-sent config.** Push and pull both read the stored per-project config; credentials stay in the
  vault, referenced by name.
- **Idempotent pull.** Re-pull only imports new external ids; nothing is duplicated.
- **Observation model generalized.** Observations can now be project-scoped without a task.

## Out of scope (later)
- **Watchers** (scheduled auto-pull → notify/create task/run playbook) on the scheduler.
- DependencyTrack/SBOM; DefectDojo pagination beyond 200 per pull; two-way status sync; modeling
  integrations as first-class capabilities.

Composes with ADR-0005 (observations→triage→findings), P10 (secrets/vault credential handling), and the
existing push connectors.
