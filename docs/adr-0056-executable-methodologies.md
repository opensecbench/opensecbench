# ADR-0056 — Executable methodologies

Status: Accepted — building (P1). A methodology should drive test execution, not just sit as a checklist a
human ticks by hand. Adopting a pack (ADR-0009 / ADR-0055) already records which checks a project intends to
run; this makes those checks *runnable*: each item declares how it's tested, "Run" fans the tests out through
the engine that already scans and triages (ADR-0035 / scan-everything), and results flow back onto the item as
coverage + evidence + findings. Builds directly on the human/agent capability parity of ADR-0053 / ADR-0054.

## Context

Coverage today is entirely manual: `MethodologyTab` lets a human set each item's status by hand, and the only
automated input is a keyword-based pack *suggestion*. `suggested_capabilities` — the one field that names the
tools that test an item — is inert: nothing reads it to launch a scan (confirmed: zero consumers in the task
engine, runner, capability, playbook, or scan-flow packages). So "this project has a methodology" means "a
checklist exists," when the operator's mental model is "these tests get run on it, and there can be several —
an AWS checklist and a web-app checklist."

The plumbing to close that gap already exists and is reusable: `Engine.ScanProject` fans capabilities across a
project's assets; `Engine.Enqueue`/`Run` run a single capability; every observation records provenance back to
its task (`Observation.TaskID`); `LinkCoverageObservation` attaches an observation to a methodology item;
`Service.Delegate` runs a specialist agent profile to completion; `events.Hub` streams to the workbench over
SSE. Two things are missing: (a) a spawned task carries no "which item asked for me" attribution, and (b) there
is no completion callback on the engine — callers poll.

## Decision

**Items carry checks.** A methodology `Item` gains `checks []Check`; a `Check` has a `kind`:
- `capability` — a deterministic scanner (`capability` id); fans out via the engine against in-scope assets.
- `agent` — a judgment check a scanner can't make; delegates to a `profile` with an `instruction`.
- `manual` — no automation; a human reviews and signs off.

An item may mix kinds. `suggested_capabilities` is superseded: it is normalized into `capability` checks (so
built-in packs and existing saved packs become runnable with no re-authoring), and the editor/agent author
`checks` directly going forward.

**"Run the methodology" fans out and routes results home.** Collect every check across the project's adopted
packs, dedupe, and dispatch by kind (capability → engine, agent → delegate, manual → await sign-off). Each
spawned unit is tagged with its originating item and a methodology-run id. On completion, a hook attaches the
resulting observations to the item as evidence and advances the item's coverage.

**Coverage means "tested," not "clean."** Running a check flips the item `not_started → in_progress → covered`
(*tested*), and attaches whatever came back. Findings are a **separate** signal shown alongside — an item can
be fully tested and still carry a critical finding. This preserves the existing meaning of coverage (did we
look here) while letting results, not clicks, answer it.

**Agent parity is absolute.** An agent-produced result flips coverage exactly as a human's does — there is no
"agent proposes, human confirms" gate. This deliberately overrides the assessor profile's earlier human-only
carve-out and follows the capability-parity principle of ADR-0053 / ADR-0054: the gate keys on an action's
consequence, not on who performs it. It falls out naturally — the result→coverage hook is indifferent to
whether a human or an agent produced the observation.

**Two new engine seams (everything else reused):**
1. *Attribution* — a methodology-run batch (modeled on `PlaybookRun`) plus an item link on the spawned
   task/agent run, so a result traces back to its row instead of being guessed from the actor string.
2. *Completion hook* — `Engine.SetOnComplete(fn)`, injected like `SetOutdatedChecker`, fired after a task
   finishes: link observations → item, set coverage, publish a `methodology.*` event on the bus.

## Phasing

Full loop (capability + agent + manual), delivered so each slice is usable as it lands:

- **P1 — Capability checks, end to end.** The check model, the two engine seams, the run orchestrator for
  capability checks, and the control-panel UI (per-item status queued → running → tested, evidence + findings,
  run all/pack/item). Proves the whole architecture on the kind that's easiest to trust.
- **P2 — Agent checks. _(done)_** The `agent` kind runs as a background sub-agent via `Service.Delegate`,
  authorizing the specialist's own toolset (automated run, no human in the loop — parity). The item id is
  carried through context (mirroring `progressSink`/`delegationDepth`), so the sub-agent's `create_observation`
  attaches its result to the item as evidence — concurrent agent checks stay correctly attributed. On
  completion the API flips coverage (covered, or in_progress if the run errored/stopped). Agent-check liveness
  isn't in the tasks table, so a small in-memory per-project tracker feeds the coverage view's RunState.
  Covers the items scanners can't (IDOR, authz, business logic).
- **P3 — Manual sign-off & polish.** Explicit human sign-off (with note) for manual items; the run surfaces
  what's waiting on a person; re-runs reconcile against prior results; report coverage reads the live registry
  (fixing the current `report.go` use of `BuiltIns()`, which drops user-authored-pack coverage from reports).

## Consequences

- The `checks` model is additive to the pack JSON, so import/export and the paste-a-checklist converter keep
  working; the converter will additionally propose a check kind per item.
- Coverage becomes partly machine-written. The `set_coverage` audit trail must distinguish a run-driven flip
  from a manual one (actor on the coverage write), so history stays legible.
- A methodology run is a new long-lived, cancelable unit of work alongside plans and scans; it appears in the
  "Running now" surface and is cancelable like them.
- Re-running is expected (assets change); the run must be idempotent-friendly — re-attaching evidence via the
  idempotent `LinkCoverageObservation` and re-flipping coverage rather than duplicating.
