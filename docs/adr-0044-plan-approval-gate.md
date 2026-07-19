# ADR-0044 — Mid-run plan approval (pausable/resumable runner)

Status: Accepted — delivered. An autonomous plan can **pause mid-run for human approval** at a declared gate
step and **resume** where it left off once a human approves — so the Analyst proposes a plan of attack and
waits before doing anything consequential, instead of running the whole DAG unattended. Completes the first
of ADR-0035's remaining autonomy items.

## Context

The plan runner (ADR-0019) executed a playbook's whole step-DAG to completion in a background goroutine with
no human checkpoint. That's fine for read-only recon, but the autonomous assessment (ADR-0035) reaches a
point — after triage, before validation — where the agent runs PoCs and sends test traffic. An assessor
wants to review the ranked plan of attack *before* that happens. There was no way to pause a run, get a
human decision, and continue.

## Decision

**A gate step (`Gate bool` on the playbook/plan step).** A gate is a human-approval checkpoint, not a
delegated task — it has no profile or instruction. When its dependencies complete and it becomes ready, the
runner parks the plan in `waiting` (step `waiting`), raises an approval notification, and **returns** —
ending the goroutine. A human's decision resumes it.

**A resume-safe runner.** `runPlan` now reloads the plan and reconstructs progress (done/failed/results)
from persisted step statuses on every entry, so relaunching it after a pause picks up exactly where it left
off — approved gates resolve as done (carrying their approval note forward as context to dependents), and the
run continues to the next gate or to completion. Same mechanism handles multiple gates in one plan.

**Resolve → resume (`ResolvePlanGate`).** A human approves or denies a waiting gate. Approve clears it
(`gate_approved`, status back to pending) so the resumed run executes past it; deny skips it, so its
dependents are skipped and the plan ends failed. The service flips the plan back to `running` and relaunches
`runPlan` on a goroutine. `gate_approved` is persisted (migration 0045) so approval survives the relaunch and
the gate isn't re-triggered.

**Surfaces.** `POST /v1/plans/{id}/steps/{stepID}/resolve` (`{approve, note}`); client `StartPlan`/`GetPlan`/
`ListPlans`/`ResolvePlanGate`; an `osb plan start|get|list|approve|deny` CLI; and an approval **notification**
(`plan:<id>` link) so the human knows a run is waiting. The `assessment` playbook (ADR-0035) gains an
`approve-validation` gate between `triage` and `validate`.

## Consequences

- **Autonomy with a human in the loop.** The Analyst can drive a full assessment but stops at the consequential
  boundary (running PoCs / sending traffic) for a human's go-ahead — propose-then-act, not act-then-explain.
  This complements the per-tool approval gate (sensitive tools) with a coarser, plan-level checkpoint.
- **Resumable by construction.** Because the runner reconstructs state from the store, a paused plan survives
  a daemon restart: on approval it relaunches and continues. There's no in-memory run state to lose.
- **Denial is a clean stop.** Denying a gate reuses the existing dependency-skip path — the gate's dependents
  skip and the plan ends failed — so "no, don't proceed" needs no special cancellation machinery.

## Out of scope — later
Editing the plan while paused (re-scope, drop steps) before resuming; a distinct `cancelled` status separate
from `failed` on denial; a workbench UI to review a paused plan and approve/deny inline; a timeout/auto-deny
for gates left waiting; parallel step execution (the runner is still sequential — see ADR-0035's remaining
items). The agent-produced deliverable (`generate_report`) is ADR-0045.

Composes with ADR-0019 (the plan runner this makes pausable), ADR-0035 (the autonomous assessment this gates),
and the sensitive-tool approval gate (per-call approval; this is the plan-level analog).
