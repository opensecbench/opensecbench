# ADR-0059 — Custom actions on findings and observations

Status: Proposed. Users can define reusable **actions** — an LLM agent or a sandboxed script — that run
against a single finding or observation, templated from its fields, with the result attached back as
evidence. The set is environment-specific (hunt our logs, generate our detection rule, draft our WAF rule),
so the platform ships editable examples and lets each operator author their own; it unions the two execution
paths that already exist (agent delegation, the sandbox runner) rather than inventing a third.

## Context

The work an assessor does *after* a finding exists is where the most environment-specific value lives, and
today none of it is expressible in-app. Concrete asks: "check our logs for anyone who actually tried this,"
"generate a Semgrep/OpenGrep rule that would catch it," "draft a WAF rule to block it." These are custom per
environment — a SIEM CLI here, a detections repo there, a house rule format — so no fixed built-in set can
cover them. What's needed is a *capability to define such operations*, with a few examples, not a hardcoded
menu.

The building blocks already exist and are the same ones a first-class feature should reuse:

- **Agent execution** — `Service.Delegate(ctx, projectID, profileID, task, authorize)`
  (`pkg/analyst/delegate.go:96`) already runs one user-defined profile against a task string to completion,
  streams its tool turns over the SSE bus, surfaces it in "Running now," and is cancelable. Saved profiles
  (`model.SavedProfile`, ADR-0018) are the ready-made "user-defined agent."
- **Script execution** — the sandbox runner (`runner.RunSpec`) with `{{param}}` templating is exactly how a
  `ContainerCapability` (`pkg/extension/capability.go:57`) turns a declared image+cmd into a gated,
  resource-limited run. A user-defined script action is structurally the same manifest with the *subject's*
  fields substituted in.
- **Results-home** — attaching a produced artifact to a subject as evidence, and streaming completion on the
  events hub, is the pattern ADR-0056 established for methodology checks.
- **Governance** — the engagement rules-of-engagement gate (ADR-0051 / consequence tiers, ADR-0054) already
  blocks a run whose technique the engagement doesn't permit. An action that reads production telemetry or
  changes state is consequential and must key on the same gate.
- **The UI seam** — `exchangeActions.ts` is the codebase's blessed "registered action" pattern
  (id/label/enabled/run), already wired to a `ContextMenu`; its own header note anticipates a plugin system
  contributing actions the same way. Findings and observations have no equivalent yet.

Nothing today lets an operator name an operation, template it from a finding, run it, and keep the output —
so this work happens out-of-band in shells and scratch files, unlinked to the finding it came from.

## Decision

Add a **Custom Action**: a saved, reusable operation an operator runs against one subject (a finding or an
observation). It is a new capability class, so it gets this ADR; it is deliberately a thin union of the two
existing execution paths.

**Model.** An `Action` record (persisted globally, like saved profiles):

- `id`, `name`, `description`, `icon`
- `kind`: `agent` | `script`
- `subject_kinds`: which surfaces it appears on — `finding`, `observation`, or both
- `applies_when`: a small predicate that filters which subjects show the button — `min_severity`,
  `statuses`, `cwe_prefixes`, `tags`. Empty = always.
- `technique`: the ROE/consequence tier (empty = passive; `intrusive`, etc.), enforced by the existing
  engagement gate before a run.
- **agent kind:** `profile_id` (a built-in or saved profile) + `instruction` (a template).
- **script kind:** `image`, `cmd []string` (templated), `network`, `timeout`, `memory`, `cpus` — the
  `ContainerCapability` shape, minus the auto-scan/disposition fields it doesn't need.
- `output`: how the result is routed (see below).

**Templating.** Both kinds substitute `{{subject.*}}` tokens resolved from the finding/observation at run
time — `{{subject.title}}`, `{{subject.severity}}`, `{{subject.cwe}}`, `{{subject.description}}`,
`{{subject.location}}`, `{{subject.status}}` — plus `{{project.environment}}` from the engagement. Agent
instructions interpolate into the task string; script `cmd` tokens interpolate as argv (subject values are
passed as argv/stdin, never shell-interpolated, so a title can't inject a command).

**Execution.** Running an action is a first-class, cancelable unit tracked in "Running now" and streamed on
the events hub, exactly like a delegated agent or a scan:

- `agent` → build the task from `instruction` + subject context and call `Service.Delegate` with the chosen
  profile, authorizing that profile's own least-privilege toolset (parity with ADR-0053/0054).
- `script` → build a `RunSpec` from the manifest with subject tokens substituted, run it in the sandbox with
  the project workspace mounted, capture stdout/stderr.

**Output routing.** Every run **always** captures its primary output to the CAS and links it to the subject
as evidence (viewable, downloadable, re-runnable) — this is the baseline. Two further, opt-in targets an
action may declare:

- **record observations** — an agent action's profile can call `create_observation` (its toolset already
  has it), so a log-hunt hit becomes a tracked observation that feeds the normal triage/dedup pipeline
  rather than a text blob. (Structured script output → observations via the existing ingest path is a
  fast-follow.)
- **write to a path** — write a produced artifact (e.g. `rule.yaml`) to a configured workspace/repo path, so
  a generated detection rule lands where the operator keeps them.

Pushing a result to an external tracker/connector is explicitly **out of scope here** — the
`/findings/{id}/push` plumbing already exists and can be wired as an output target in a later ADR.

**Surfaces.**

- A new **Custom Actions** section in the Library (beside Custom Agents and Playbooks) is the authoring home:
  create/edit/delete, pick kind, write the template, set `applies_when` and `technique`, with a live preview
  of the rendered instruction against a sample subject.
- On a finding and on an observation, an **Actions ▾** menu (and the row right-click) lists only the actions
  whose `subject_kinds`/`applies_when` match. This is a new `customActions.ts` registry mirroring
  `exchangeActions.ts`, plus an `onRowContextMenu` on the shared `DataTable`.

**Naming.** The feature is "Custom Actions"; the on-subject control is "Actions." "Action" is qualified in
code (`pkg/action`) to keep it distinct from disposition *actions* (ADR-0028) and exchange *actions*.

**Shipped examples** (editable built-ins, all agent kind): *Hunt logs for abuse*, *Generate OpenGrep rule*,
*Generate WAF rule*. They exist to be cloned and tailored — an operator points the log-hunt at their SIEM and
the rule generators at their house format.

## Phasing

- **P1 — Both kinds, end to end.** The `Action` model + store + migration; the Library authoring UI; agent
  and script execution wired through `Delegate` and the sandbox; the ROE gate; evidence output; the Actions
  menu on findings *and* observations; the three example actions.
- **P2 — Richer output.** `record observations` for script output (via ingest) and the `write to a path`
  target; per-project enable/override of the global set.
- **P3 — Distribution & connectors.** Actions contributed by extension packages (extend the manifest shape,
  ADR-0013); tracker/connector push as an output target.

## Consequences

- The two execution paths are reused, not duplicated: an agent action is a thin wrapper over `Delegate`, a
  script action a thin wrapper over the same `RunSpec` sandbox a `ContainerCapability` uses. New surface is
  mostly the record, its CRUD/run API, and the UI.
- Actions are user-authored executables that run against real environments; the ROE gate is load-bearing, not
  decorative. Passive actions (generate a rule from code already in hand) stay ungated; anything that reads
  telemetry, sends traffic, or writes state declares a technique and is blocked unless the engagement permits
  it. Script `cmd` interpolation passes subject values as argv/stdin, never through a shell.
- Global-by-default persistence matches saved profiles/playbooks and keeps an action reusable across
  projects; per-project scoping is deferred to P2 (the disposition per-project override is the model to
  follow).
- A finding/observation gains an action-run history — another long-lived, cancelable unit alongside plans,
  scans, and methodology runs — and re-running is expected and idempotent-friendly (re-attach evidence, don't
  duplicate).
- Extensions can't contribute actions yet (P3); until then the built-in examples plus operator-authored
  actions are the whole set. The manifest is designed so the extension shape is additive when it lands.
