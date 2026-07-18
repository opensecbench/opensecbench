# ADR-0005 — Evidence interpretation & finding lifecycle

Status: Accepted

## Context

Tool output (e.g. Semgrep SARIF) and, later, AI reasoning must become *reviewable* evidence
without silently becoming findings. ADR-0002 fixes the entities (observation, evidence, finding)
and the rule that AI conclusions never silently become evidence. This ADR fixes the lifecycle and
the interpretation mechanism.

## Decision

**Interpreters turn an artifact into observations.** An interpreter reads an output artifact and
emits `observation` rows, each carrying its `origin` and a `review_state` of `unreviewed`, linked
to the producing task and source artifact.

- **Deterministic interpreters first** (this phase): a SARIF parser maps each tool result to one
  observation (`origin = tool`). No interpretation is inferred beyond what the tool reported.
- **LLM interpreters later** (P4): emit observations with `origin = thread`, clearly marked.

**The engine auto-interprets** an output whose media type has a registered interpreter (SARIF →
observations) immediately after storing the artifact, so running a SARIF-producing capability
yields reviewable observations in one step.

**Humans review observations** — `confirmed` or `rejected`. Only a human transition unlocks an
observation for use in a finding.

**Findings are assembled from confirmed observations.** A finding records the security conclusion
and is `supported_by` one or more observations (join table), giving the chain
`finding → observation → (task, artifact) → capability+version, runner`. Neither a tool nor the
agent creates a finding directly; a person does, from reviewed evidence.

## Consequences

- `origin` and `review_state` are enforced at the boundary: an unreviewed or rejected observation
  cannot support a finding.
- Interpreters are keyed by media type, so a new tool that emits SARIF reuses the parser; other
  formats add their own interpreter.
- The full `evidence` entity (fragment/selection tagging from ADR-0002) is deferred; in this phase
  findings link observations directly. Fragment-level evidence tagging is tracked for a later phase.
- Retest/dedup across engagements builds on this model later; the lifecycle here is the foundation.
