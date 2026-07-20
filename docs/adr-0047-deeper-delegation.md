# ADR-0047 — Deeper delegation + configurable sub-agent budget

Status: Accepted — delivered. Delegation can now **nest more than one level** — a coordinator specialist
(the pentester) can itself delegate a separable sub-task to another specialist — bounded by a depth cap so
the delegation tree stays finite. The delegated sub-agent's **tool-turn budget** is also raised and made
configurable. The last of ADR-0035's remaining autonomy items (with the parallel scheduler, ADR-0046).

## Context

Delegation (ADR-0019) was deliberately one level deep: only the Lead held the `delegate` tool, so a
specialist couldn't decompose further. That kept the tree trivially finite, but it meant a broad engagement
run by the pentester couldn't hand a focused, separable piece (a scan pass, a documentation-research pass, a
report write-up) to the right specialist — it had to do everything itself within a single sub-agent loop.
That loop was also capped at **8 tool turns**, which is tight for a real sub-task (a recon or scan step
easily exceeds a handful of tool calls before it can answer).

## Decision

**Depth-bounded deeper delegation.** Delegation depth rides on the context: `Delegate` runs its sub-agent one
level deeper (`withDelegationDepth(ctx, depth+1)`), and `runDelegate` (the `delegate` tool handler) refuses a
call once depth has reached `maxDelegationDepth` (default 3, `OSB_AGENT_MAX_DEPTH`) — the sub-agent is told to
finish the work itself rather than delegate again. Bounded depth × the existing concurrency cap (`agentSem`)
keeps the whole delegation tree finite: no runaway.

**Which profiles delegate.** The `delegate` tool is added to the **pentester** (a broad-authority coordinator
that benefits from decomposing) alongside the Lead. The narrow specialists (code-analysis, vuln-validator,
report-writer, knowledge-scribe, …) still don't hold it — deeper delegation stays with coordinator roles, and
`delegate` remains a **sensitive, approval-gated** tool (ADR-0019 §5), so a human still authorizes each
spawned sub-agent's toolset.

**Configurable sub-agent budget.** A delegated sub-agent's `MaxSteps` is raised from 8 to **16** and made
configurable (`OSB_AGENT_MAX_STEPS`) — enough room to complete a real sub-task. The interactive session's
per-turn cap is unchanged (that's a responsiveness bound, a different concern). The concurrency env override
was folded into a shared `envInt` helper.

## Consequences

- **Recursive decomposition, safely.** The pentester can now split a large engagement across specialists —
  Lead → pentester → (scan / research / report specialist) — while the depth cap guarantees the tree can't
  grow without bound. Each spawned sub-agent is still approval-gated and scope/DLP-bounded.
- **Sub-agents can actually finish.** Raising the turn budget removes the common failure where a sub-task hit
  the 8-turn wall mid-way; 16 (tunable) fits real recon/scan/triage work.
- **No governance regression.** `delegate` stays sensitive and gated; only a coordinator profile gained it;
  depth and concurrency are both capped. The "no runaway trees" guarantee is preserved — now as a bound
  rather than a hard one-level limit.

## Out of scope — later
Per-profile or per-playbook depth/budget overrides; letting more profiles delegate (kept to the pentester for
now); a delegation **trace** surfaced in the UI (who delegated what, how deep); dynamic budgets that scale
with task size; a pipelined plan scheduler (noted under ADR-0046). Fanning the built-in playbooks out to
exploit the parallel scheduler + deeper delegation is a natural follow-up.

Composes with ADR-0019 (the delegation primitive + the sensitive-tool gate this preserves), ADR-0035 (the
autonomous assessment that benefits), and ADR-0046 (parallel plan steps — the other half of richer autonomy).
