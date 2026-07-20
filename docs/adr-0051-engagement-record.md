# ADR-0051 — Engagement record & setup modal (guardrails, not just paperwork)

Status: Accepted — building (phased). Project creation moves from "name + template" to an **engagement
setup modal** that captures the frame of an assessment: identity, scope + authorization, rules of
engagement, timeline, contacts, access, and reporting. The safety-critical parts are **enforced**, not just
recorded — out-of-scope feeds the scope guard, prohibited techniques gate capabilities, and a data-sensitivity
class tightens external-LLM egress for that engagement.

## Context

`model.Project` today holds only id, org/group, name, status (active|archived), target links, and timestamps
(`pkg/model/model.go:31`). Everything about an engagement is either added later by navigating to a sub-surface
(scope, assets, methodology, integrations), is a **global** posture (egress policy, approval policy, model
routing), or is **not modeled at all**. Not modeled: engagement type/objective/reference, testing window,
out-of-scope exclusions, authorization record, rules of engagement, points of contact, test accounts,
severity/standard. Scope (`scope_entries`) is an **allowlist only** — there is no "do not touch." Templates
scaffold at most one application and do **not** adopt methodology or seed scope (ADR-0009/templates).

The result: a new engagement starts empty and unsafe-by-default (empty scope = allow-all, `pkg/scope/scope.go`),
and the operator must click across many surfaces to set it up. For an *agentic* security-assessment platform
(SAST, secrets, SCA, findings/dispositions, methodology, HTTP toolset) this is also a missed control point: the
engagement frame is exactly the policy an autonomous agent should be bound by. The frame generalizes across
assessment kinds — a code audit, an application assessment, a vulnerability assessment, a cloud review — not
only active/intrusive testing; the intrusive-technique constraints are one optional facet, not the core.

## Decision

**1. An engagement record, per project.** A 1:1 `engagement` row in the project database (ADR-0049), plus two
child tables, holds the frame:

```
engagement(project_id PK, kinds, objective, reference, environment, data_class, standard, compliance,
           severity_scale, authorized, authorizer, auth_ref, auth_from, auth_to,
           window_start, window_end, report_due, techniques(JSON), notes, created_at, updated_at)
engagement_contacts(id, project_id, role[technical|authorizer|breakglass], name, email, phone, note)
engagement_test_accounts(id, project_id, role, username, secret_ref→vault, note)
```

`kinds` is a comma list (an engagement can be web+api). `techniques` is a JSON map of allow-flags
(`intrusive`, `automated_exploit`, `brute_force`, `dos`, `social`, `destructive`). Test-account passwords are
never stored here — only a `secret_ref` into the existing vault (ADR-0011). Added to **both** schema tracks
while they coexist: legacy `migrations/0047_engagement.sql` and two-tier `migrations/project/0002_engagement.sql`.

**2. Out-of-scope is first-class.** `scope_entries` gains `disposition TEXT DEFAULT 'allow'` (allow|deny).
Out-of-scope entries are `deny` rows of the same kinds (host|domain|cidr). `scope.Check` gives **deny
precedence**: a value matching any deny entry is blocked even if an allow also matches. Empty allowlist stays
allow-all for back-compat, but a deny always wins.

**3. The setup modal owns the frame; deep config still lives in its surfaces.** The modal captures identity,
scope/authorization, RoE, and kickstart, and — at creation — seeds what already has a home: writes scope
allow/deny rows, creates the first application + asset from a repo/URL, adopts the chosen methodology packs
(which templates never did), and links an issue tracker. Individual assets, per-item coverage, and the
credential vault remain their own surfaces; the modal seeds, it does not replace them. Editable afterward via
a Project Settings surface bound to `GET/PUT /v1/projects/{id}/engagement`.

**4. Enforcement points (the "guardrails" promise), phased.**
- **Scope deny → scope guard** (Phase 1, with the record): out-of-scope blocks Replay/proxy/scan targets.
- **Data class → egress** (Phase 2): `restricted` makes the project behave as the `strict` egress profile
  regardless of the global default — no private evidence to external providers (ADR-0006/0011).
- **Techniques → capability gating** (Phase 3): capabilities carry a technique tag; a capability whose tag is
  not allowed for the engagement is blocked at enqueue and hidden in the Scan UI. Needs a `technique` field on
  the capability manifest; until tagged, capabilities are unconstrained (fail-open with a visible note).

## Phasing

1. **Record + modal + scope-deny** — model, migrations (both tracks), store CRUD, `createProject` accepting an
   engagement payload, `GET/PUT /v1/projects/{id}/engagement`, the full modal, and scope deny-precedence.
2. **Egress by data class** — policy resolution consults the project's `data_class`.
3. **Capability technique gating** — manifest `technique` tag + enqueue/UI gate.

## Non-goals / notes

- **Group tier** stays unimplemented (SQL-only, no Go type) — out of scope here.
- **No approval-policy per project** yet — the trust-curve gate (ADR-0044) remains global; only egress gets a
  per-engagement tightening, and only tighter, never looser than the global posture.
- **Authorization is a record + a soft gate**, not cryptographic proof: unauthorized (or expired) shows a
  banner and can warn before runs; it does not currently hard-block, to avoid trapping mid-engagement work.
- **Back-compat**: every engagement field is optional; a project with no engagement row behaves exactly as
  today. `createProject` with no engagement payload is unchanged.
