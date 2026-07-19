# ADR-0019 — Agent profiles & lead-agent orchestration

Status: Proposed. Task-specialized agent **profiles** (report writer, vuln validator, code analysis,
pentester, …) with least-privilege toolsets, driven by a **lead agent** that routes a request to the right
specialist and lets specialists hand work to each other. Built-in profiles now; DB/extension-defined later.

> **Note (design evolved during co-design — this doc predates the changes and will be revised when this phase
> begins).** Confirmed refinements not yet folded in below: orchestration is **human-triggered and adaptable,
> never autonomous or looping** — work starts from an interactive ask or a **playbook** (button) that first
> reads existing state (assets, prior scans, KB) and does only what's missing, then stops; playbooks default
> to **goal + adaptable steps, editable**, and a completed run can be **recorded as a reusable playbook**.
> Approval is a **trust-curve policy** (conservative → relax rule-by-rule; `tool [+ profile] → auto|approve`),
> not the fixed per-delegation gate in §5, over an immovable scope/DLP floor. The work has a **deliverable
> arc** (findings → triage → phased reports) and supports **scheduled baseline→delta + finding-retest** runs.
> **Human ⇄ agent parity** is a hard invariant. Full current design: the co-design brief. Depends on ADR-0020.

## Context

Today there is exactly one agent: `buildSystemPrompt()` is a single hardcoded persona and every thread gets
the full 17-tool catalog (ADR-0017). That persona does two jobs — **safety invariants** (no fabrication,
tool-results-are-untrusted, no raw shell) and a bland **generalist identity**. Two problems:

1. **Behavior.** One generalist has no task framing. Real assessment work is a set of distinct jobs —
   writing findings, validating a suspected vuln, analyzing source, active testing — each wanting different
   instructions, tone, and rigor.
2. **Privilege.** Every agent can call every tool. A "report writer" can `send_request` and `run_capability`;
   a "code analyzer" can fire live traffic. That is neither safe nor focused.

The runtime is already most of the way there: `Loop`/`Session` take `Tools []Tool` and a system prompt as
inputs — they are simply hardwired to the one persona + full catalog. The canonical tool-message model,
approval gate, scope guard, audit, and DLP already operate per tool call and are reused unchanged.

## Decision

### 1. The Profile

```
Profile { ID, Name, Description; Persona string; Tools []string /*allow-list*/; Model, MaxSteps (optional) }
```

A profile supplies a **task persona** and an **allow-list** naming a strict subset of the tool catalog. The
loop offers only that subset, so a tool a profile lacks is *not callable* — least privilege by construction,
not by instruction. Built-in profiles live in a `Profiles()` registry in `pkg/analyst`; DB overrides and
extension-pack profiles (ADR-0013 trust model) come later, so the shape must anticipate them.

### 2. Safety invariants are shared and non-overridable

The system prompt is `sharedInvariants + profile.Persona`. The invariants (anti-fabrication,
treat-tool-results-as-untrusted, no-raw-shell) are prepended for every profile and cannot be dropped. A
profile can never widen the gate, add a tool outside the catalog, or remove an invariant. Profiles change the
*task*, never the *guardrails*.

### 3. Built-in profiles

| Profile | Tools (allow-list) | Denied (notable) |
|---|---|---|
| **Lead** (orchestrator, default entry) | triage reads (`list_*`, `search`, `get_finding`, `get_coverage`) + `delegate` | all outbound/exec — the Lead never acts directly, it delegates |
| **Report Writer** | reads + `search` `get_finding` `get_coverage` `draft_kb_entry` `create_finding` | `send_request` `run_capability` `run_playbook` |
| **Vuln Validator** | `get_finding` `list_exchanges` `get_exchange` `send_request` `run_capability` `create_finding` | `run_playbook` `set_coverage` |
| **Code Analysis** | `list_assets` `run_capability` `search` `get_finding` `draft_kb_entry` | `send_request` (no live traffic) |
| **Pentester** | reads + `send_request` `run_capability` `run_playbook` `set_coverage` `create_finding` | — (broadest; every mutating call gated) |
| **Generalist** | all 17 (today's behavior) | — (compatibility / escape hatch) |

### 4. Lead-agent orchestration via a `delegate` tool

Delegation is **just another tool**, so it reuses the whole loop/gate/audit machinery rather than adding a
parallel orchestrator. The **Lead** profile's toolset is triage reads + one gated tool:

`delegate(agent: <profile id>, task: <natural-language sub-task>)`

Executing `delegate` spawns a **sub-agent** — a fresh `agent.Loop` with the target profile's persona + tools,
seeded with `task` as its user message — runs it to completion, and returns its final answer (plus a short
summary of the tools it used) as the tool result to the Lead. The Lead reads that and decides what to do next:
answer, or delegate again. Example: *"validate this XSS then write it up"* → Lead → `delegate(vuln-validator,
…)` → (proof) → `delegate(report-writer, …)` → (finding). **Hand-off** is the Lead delegating again after a
specialist returns; specialists do **not** get `delegate` initially (keeps the tree shallow and legible).

- **Depth cap**: delegation nests at most 1 level (Lead → specialist), no specialist-of-specialist, in phase
  one. A hard cap prevents runaway trees regardless.
- **Audit**: every `delegate` and every tool call inside the sub-agent is recorded, tagged with the acting
  profile, so the full tree is reconstructable.

### 5. Gating under delegation — the load-bearing decision

A sub-agent may want gated tools (`send_request`, `run_capability`, `create_finding`). Our approval model
pauses a run and asks a human. Re-prompting *inside* a synchronous sub-agent would mean a nested pause/resume
stack — a real re-architecture. Instead:

**`delegate` is the gated unit, and approving it authorizes the specialist's declared gated tools for that
one sub-task.** The approval card shows what it grants: *"Lead → Vuln Validator: may use send_request,
run_capability. Approve?"* On approval, the sub-agent runs to completion with exactly those tools
pre-authorized (reusing `Approver(allow)`); any tool outside the grant is denied; and **every per-action guard
still fires** — `send_request` is still scope-checked, DLP still applies, everything is still audited. Granting
is **per-delegation** (a named, reviewable unit that names the powers it confers), not per-action.

The trade-off: a human approves a *bounded sub-task with a declared tool set*, not each individual request the
specialist makes within it. That is coarser than today's per-call gate, but it is explicit, scope-bounded, and
fully audited — and it is the only model that keeps sub-agents synchronous. Per-action approval inside a
delegation (the nested-resume design) is deferred until we know we need it.

### 6. Persistence

A thread records its **entry profile** (`agent_type`, default `lead`) so it is stable across turns and resume.
The delegation call and its result are ordinary canonical tool turns (ADR-0017), so the sub-agent's work
persists as the Lead's transcript; the sub-agent's own step transcript is captured in the tool result /
audit for drill-down. No new message model — just a column and the `delegate` tool.

## Consequences

- **Focused + least-privilege.** Each agent is framed for its job and physically cannot exceed its toolset.
- **Reuses everything.** Orchestration is a tool; sub-agents are loops; gating/scope/DLP/audit/persistence are
  unchanged. The only genuinely new runtime concept is "a tool that runs a sub-agent," plus a depth cap.
- **Governance is explicit but coarser under delegation** (§5) — documented, scope-bounded, audited; finer
  per-action gating inside delegations is future work.
- **Phasing:**
  1. Profile model + shared-invariants prompt + the built-in specialists with restricted toolsets; thread
     `agent_type`; select a profile directly (substrate; also the manual/advanced escape hatch).
  2. The **Lead** profile + `delegate` tool + gating-under-delegation (§5) + depth cap + delegation audit.
     Lead becomes the default entry; talking to the Analyst routes automatically.
  3. Specialist-to-specialist hand-off (raise the depth cap thoughtfully) + a delegation-tree view in the UI.
  4. DB/extension-defined profiles (ADR-0013 trust model); optional per-role model selection.
- **Out of scope now:** per-action approval inside delegations; parallel/concurrent sub-agents; a profile
  editing UI.

Builds on ADR-0006 (agent runtime) and ADR-0017 (tool-aware providers, canonical tool messages, the governed
toolset). Coordinates with ADR-0001 (scope), ADR-0011 (DLP), ADR-0013 (extension packs, for future profiles).
