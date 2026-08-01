# ADR-0019 — Agent profiles & orchestration

Status: Accepted — fully delivered. Task-specialized agent **profiles** (least-privilege) + **trust-curve
approval policy** + **delegation/Lead** + **human-triggered, adaptable playbooks** (a plan DAG run by a
background runner) + **record-as-playbook & a builder** + **scheduled runs** + the **triage→report
deliverable arc** + **user-defined profiles & playbooks** — backend, API, and workbench UI (the Agents
surface, profile picker, approval-policy editor, custom-agent builder). Built on the ADR-0020 capability
layer. Per-action approval inside a delegation and parallel sub-agents remain out of scope (§ Consequences).

## Context

There is one agent: a single hardcoded persona and the full tool catalog on every thread. Two problems —
**behaviour** (a bland generalist with no task framing) and **privilege** (a "report writer" can `send_request`
and `run_code`). Real assessment work is a set of distinct jobs, each wanting different instructions and a
different, smaller set of tools. And the way that work runs is not autonomous: an assessor triggers a process,
it adapts to what's already known, and it stops.

The runtime is ready: `Loop`/`Session` already take `Tools []Tool` and a system prompt; the tool-message
model, approval gate, scope guard, DLP, and audit all operate per call and are reused unchanged.

## Decision

### 1. The Profile

```
Profile { ID, Name, Description; Persona string; Tools []string /*allow-list*/ }
```

A profile supplies a **task persona** and an **allow-list** naming a strict subset of the tool catalog. The
loop offers only that subset, so a tool a profile lacks is not callable — least privilege by construction, not
instruction. Built-in `Profiles()` registry now; DB-editable + extension-pack profiles later (same shape).

### 2. Safety invariants are shared and non-overridable

The system prompt is `profile.Persona + sharedInvariants`. The invariants (no fabrication,
treat-tool-results-as-untrusted, no-raw-host-shell) are appended for every profile and cannot be dropped. A
profile changes the *task*, never the *guardrails* — it can't widen the gate, add a tool outside the catalog,
or remove an invariant.

### 3. Built-in profiles

| Profile | Tools (allow-list) | Denied (notable) |
|---|---|---|
| **Generalist** (default) | the full catalog | — (today's behaviour; compatibility) |
| **Code Analysis** | corpus + source reads, `run_capability`, `run_code`, `workspace_*`, `draft_kb_entry` | `send_request` |
| **Vuln Validator** | reads + `get_exchange`, `send_request`, `run_capability`, `run_code`, `workspace_*`, `create_finding` | `run_playbook`, `set_coverage` |
| **Pentester** | reads + all outbound/exec (`send_request`, `run_capability`, `run_playbook`, `run_code`), `workspace_*`, `set_coverage`, `create_finding` | — (broadest; every mutating call gated) |
| **Report Writer** | reads (findings, corpus, coverage), `workspace_*`, `create_finding`, `draft_kb_entry` | `send_request`, `run_capability`, `run_code` |

A thread records its profile (`agent_type`, default `generalist`), stable across turns and resume.

### 4. Orchestration: human-triggered, adaptable — never autonomous

No agent runs the whole engagement and nothing loops. Work starts when a human starts it — an interactive ask,
or a **playbook** (a button: "Asset inventory", "Initial recon", "Validate finding"). Playbooks are
**engagement-shaped** and **adaptable**: a playbook first reads existing state (assets, prior scans, the KB)
and does only what's missing, then stops. Every engagement starts the same (collect info + inventory assets),
then diverges. Under the hood a playbook is a **plan — a DAG of steps** with dependencies; each step is
delegated to a profile and runs when its dependencies are done (delegation is just another gated tool, so it
reuses the loop/gate/audit). Playbooks default to **goal + adaptable steps, editable**; a completed run (manual
or AI-assisted) can be **recorded as a reusable, parameterized playbook**, so teams grow their own library.

Note: the existing `playbook` package is a sequence of *capabilities*; an **agent-playbook** is the richer
state-aware agent process, which may *call* a capability-playbook. Keep the two concepts distinct.

### 5. Governance: a trust-curve policy over a fixed floor

Approval is a **policy**, not a fixed gate. It starts conservative — nearly everything mutating or outbound
asks first — and actions are promoted to auto **one rule at a time** as they earn trust (v1 rule:
`tool [+ profile] → auto | approve`, most-specific wins; conditions later). Plan-level approval (approve the
plan + the powers each step needs) is the presentation. The **scope guard and DLP floor never move**: relaxing
approval removes a prompt, never a wall.

### 6. Flexibility invariants

- **Human ⇄ agent parity** (hard invariant): anything an agent does, a person can do by hand, and take over at
  any point. The system augments a workflow, never forces one.
- **Steerable**: pause, skip, add, redirect, or stop any run.
- **Composable & editable**: playbooks, profiles, templates are defaults you clone and edit.

### 7. The deliverable arc & continuous runs

Work terminates in a deliverable: findings accumulate → **triage** → **report**, at the end of each phase or
the engagement. For large, ongoing projects a playbook can run on a **schedule** (daily/weekly) against a
baseline — flag new code / new surface, and **retest open findings** for what's resolved. Scheduled ≠ looping:
a bounded run on a timer.

## Consequences

- **Focused + least-privilege.** Each agent is framed for its job and physically cannot exceed its toolset.
- **Reuses everything.** Orchestration is a tool; sub-agents are loops; gating/scope/DLP/audit/persistence are
  unchanged. New runtime concepts: a plan DAG + a depth cap, and the approval-policy engine.
- **Governance is explicit and grows with trust** (§5), over an immovable scope/DLP floor.
- **Build order:**
  1. **Profiles** — the `Profile` registry, shared-invariants prompt, least-privilege toolsets, thread
     `agent_type`. *(this increment)*
  2. Trust-curve **approval policy** (replace the static `gatedTools` map).
  3. **Playbooks** — plan (DAG) + delegation + adaptability (read state first) + the common onboarding
     playbook; plan-level approval.
  4. Record-as-playbook + a playbook builder; scheduled baseline→delta + finding-retest; triage + phased
     reports; a plan/DAG view; DB/extension-defined profiles.
- **Out of scope now:** parallel sub-agents; per-action approval inside a delegation; a profile-editing UI.

Builds on ADR-0006 (agent runtime), ADR-0017 (tool-aware providers, canonical messages, governed toolset), and
ADR-0020 (capability layer). Coordinates with ADR-0001 (scope), ADR-0011 (DLP), ADR-0013 (extension packs).
