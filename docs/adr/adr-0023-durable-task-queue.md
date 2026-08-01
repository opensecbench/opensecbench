# ADR-0023 — Durable task queue

Status: Accepted — delivered. The task queue is persisted: the `tasks` row **is** the queue. Pending work
survives a control-plane restart, and tasks interrupted mid-run are resumed (at-least-once, capped).
Supersedes the in-process-queue limitation of ADR-0022.

## Context

ADR-0022 made capability runs asynchronous via an **in-process** channel (`jobs chan job`) drained by a
worker pool, and documented the trade-off: the queue was not persisted, so a control-plane restart
reconciled any queued-but-unstarted work to *failed*. For a local-first, single-user workbench that
restarts often (app update, crash, laptop sleep), a user who kicks off a batch of scans and restarts
loses the queued ones. A durable queue removes that cliff.

A data-flow trace confirmed everything needed to re-run a queued task is already on the `tasks` row or
deterministically re-derivable via `Engine.prepare()` (asset lookup + a pure `Capability.Plan`) —
**except** the secret-reference map (`SecretRefs`, envVar→vault-secret *name*) and a raw `TargetDir`
passed without an asset, both previously in-memory only.

## Decision

**The DB is the queue.** Replace the in-memory channel with atomic claims against the `tasks` table.
`Enqueue` records a `pending` task and nudges a wakeup channel; workers atomically claim the oldest
pending row (flip → `running`, stamp `started_at`, bump `attempts`), reconstruct the `RunRequest` from
the row, re-`prepare()`, and `execute()`. Because the row is the queue, fresh and restart-resumed tasks
follow the identical claim→reconstruct→run path, and there is no second source of truth to drift.

- **Atomic claim** (`ClaimNextPendingTask`): one statement — `UPDATE tasks SET status='running', … ,
  attempts=attempts+1 WHERE id=(SELECT id … WHERE status='pending' ORDER BY created_at LIMIT 1) AND
  status='pending' RETURNING …`. The guarded `AND status='pending'` makes concurrent claims safe on
  SQLite's single writer (WAL + `busy_timeout`); a loser simply finds nothing and waits.
- **Low latency + backstop**: a buffered `notify` channel wakes a worker immediately on enqueue; a short
  per-worker poll (`pollInterval`, 1s) is the belt-and-braces path that also picks up restart-resumed and
  any missed-signal work.
- **Reconstruction columns** (migration 0033): `secret_refs` (JSON envVar→vault name), `target_dir` (raw
  dir when no asset), `attempts`. The `RunSpec` is **not** persisted — re-derived by `prepare()` at claim
  time (pure `Plan`). Secret *values* are never stored (ADR-0011); only reference names are.

**Restart semantics (at-least-once).** On startup, `RequeueInterruptedTasks` resets `running` → `pending`
(so mid-run tasks resume); `pending` tasks resume as-is. `attempts` is preserved across requeue, so a
**retry cap** (`maxAttempts`, default 3, `OSB_TASK_MAX_ATTEMPTS`) fails a task that keeps crashing the
process instead of looping forever. This is the user-chosen policy: convenience of resume, bounded by the
cap. The alternative (fail interrupted runs) was rejected as too lossy for a restart-heavy local tool.

**Cancellation** simplifies to no in-memory bookkeeping: a running task → `docker kill` + context cancel
(unchanged); a pending task → `CancelPendingTask`, a guarded `UPDATE … status='failed' … WHERE
status='pending'` so a worker never claims it.

**Scope.** Durability covers single **capability tasks** (the `Enqueue` path). Playbook runs and agent
plans keep ADR-0022's behavior — reconciled to failed on restart. Resuming a half-finished playbook is a
DAG-cursor problem for a separate ADR.

## Consequences

- **Queued work survives restarts.** Enqueue a batch of scans, restart the control plane, and the queued
  ones resume to `succeeded` instead of vanishing as `failed`.
- **Single source of truth.** No channel/DB drift; the same code path serves fresh and resumed tasks.
- **At-least-once for interrupted runs.** A side-effecting capability (e.g. a network probe) that was
  mid-run when the process died may execute more than once. The retry cap bounds runaway re-runs; users
  who need exactly-once should prefer idempotent capabilities. (Never-started `pending` tasks are claimed
  exactly once by the atomic guard.)
- **Secret reference names are persisted** (never values) so secret-injecting tasks are durable; they
  resume only if the vault can resolve those names at exec time (fail-closed otherwise, per ADR-0011).
- **Capability-version drift**: a task enqueued before a capability was upgraded/removed re-plans against
  the *current* registry on resume; an unknown capability fails the task cleanly.
- **In-process worker pool, DB-durable queue.** The queue persists, but the workers do not run while the
  process is down — an in-flight container is orphaned by a crash; its task is requeued and re-run.
- **No client or API change.** The 202 + poll contract (ADR-0022) is unchanged; durability is transparent
  to the desktop app.

Composes with ADR-0004 (sandboxed runner unchanged), ADR-0011 (secret values still never persisted), and
ADR-0022 (this makes that queue durable).
