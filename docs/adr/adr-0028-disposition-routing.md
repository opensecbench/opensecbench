# ADR-0028 — Post-run disposition routing + investigations

Status: Accepted — delivered. After a capability's output is interpreted into observations, tool-declared
**disposition rules** route each to an action: auto-promote to a **finding**, open a tracked
**investigation**, or leave it for manual **review**. Generic (any tool declares its own routing);
TruffleHog ships verified→finding, unverified→investigate.

## Context

A capability's only post-run step was a hardcoded interpreter switch producing **unreviewed** observations;
nothing ran after, and everything waited for manual triage. There were no pre/post-run hooks. TruffleHog's
`verified` flag was even lost — folded into severity, unrecoverable downstream. James wanted verified
secrets to become findings immediately and unverified ones to spawn an investigation (agent + testing,
always human-validated) — generically, so other tools can do the same.

## Decision

**Structured observation attributes.** Observations gain `attributes map[string]string` (JSON column) so
interpreters carry facts rules match on. TruffleHog now sets `verified` and `detector`.

**Disposition rules, tool-declared.** `pkg/disposition` is a pure matcher: `Disposition{When
map[string]string, MinSeverity, Action}`; `Evaluate(obs, rules)` returns the first matching rule's action
(all `When` keys equal the observation's attributes, severity ≥ `MinSeverity`), else `review`. A capability
declares defaults in its **manifest** (`capability.Manifest.Dispositions` / `extension.ContainerCapability`,
so an extension.json ships them); a project can **override** via `disposition_rules` (consulted first).
The trufflehog extension.json declares `[{when:{verified:"true"}→finding},{when:{verified:"false"}→
investigate}]`.

**Applied post-interpret** (`engine.applyDispositions`, right after observations are created). For each
observation: `finding` → confirm it + `CreateFinding` (auto-satisfying the confirmed-observations
invariant); `investigate` → `CreateInvestigation` (a tracked work-item, unique per observation); `review`
→ nothing. Each action appends an audit line (actor `disposition`). Best-effort — routing never fails the
task; a capability with no dispositions behaves exactly as before.

**Investigations are human-triggered.** A disposition only *queues* an investigation (TruffleHog can emit
many hits — no surprise agent fan-out). `POST /v1/investigations/{id}/run` starts a **vuln-validator**
thread seeded with the observation ("validate this; propose a finding if real — which needs my approval").
The agent reasons + tests using existing tools; any `create_finding` it makes is already human-gated
(ADR-0019 §5), so a person always validates. `status` resolves/dismisses.

## Consequences

- **Verified secret → finding, instantly.** No manual triage for high-confidence results.
- **Unverified → a tracked investigation**, worked on demand by a human and/or a seeded agent, ending in a
  human-validated finding. The example/placeholder-vs-real judgement is exactly where the agent + human help.
- **Generic + tool-owned.** Any capability declares its own routing in its manifest; projects override.
  The mechanism is not TruffleHog-specific.
- **Observation model generalized.** Structured attributes (also useful for future routing on
  confidence/service/etc.). Adding an attribute-carrying interpreter is still a new case + the attributes.
- **No regression.** Capabilities without dispositions still create plain unreviewed observations.

## Out of scope (later)
- Auto-spawn investigations (kept human-triggered by decision); a dedicated validate-secret playbook (a
  seeded vuln-validator thread suffices); pre-run hooks; an interpreter plugin registry; dispositions for
  tools beyond TruffleHog (they just declare rules).

Composes with ADR-0005 (observations→triage→findings), ADR-0013/0021 (manifest-declared extension
capabilities/settings — dispositions are the same shape), and ADR-0019 (the human-gated agent).
