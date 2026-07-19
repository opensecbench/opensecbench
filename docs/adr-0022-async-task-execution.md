# ADR-0022 — Asynchronous capability execution

Status: Accepted — delivered. Capability runs are **enqueued** and executed by a **bounded worker pool**
in the background; `POST /v1/tasks` returns immediately with a pending task, and clients poll for
completion. Runs survive client disconnect, and a burst of scheduled/triggered runs queues rather than
overloading the host.

## Context

The task engine's `Run` was synchronous: `POST /v1/tasks` blocked the HTTP request for the *entire*
capability duration — up to a capability's `timeout_seconds` (e.g. 600s for TruffleHog). Three problems:

- **Client-coupled lifecycle.** The run used the request context, so a client disconnect (closed tab,
  network blip) cancelled the run mid-flight.
- **No queue, no backpressure.** Every run started immediately in the handler goroutine. Scheduled or
  triggered runs (ADR-0019) could fan out into many concurrent containers with nothing bounding them.
- **No queued state.** A task only existed once `Run` created it and blocked; there was no visible
  "queued" phase, and the cockpit couldn't show work waiting to start.

The Analyst already runs asynchronously (resumable threads, a sub-agent semaphore). Capability execution
should match that posture.

## Decision

**Enqueue + worker pool.** The engine gains an in-process job queue drained by a fixed pool of workers
(`OSB_TASK_WORKERS`, default 3 — mirrors `OSB_AGENT_MAX_CONCURRENT` for sub-agents). Runs execute on the
engine's **own background context**, not the request's, so they survive client disconnect. `Close()`
cancels that context and drains the pool on shutdown.

**Split the run.** `Run` is factored into three reusable parts:
- `prepare` — validate the capability, resolve the asset/target, and plan the spec. No task, no
  container. Bad requests (unknown capability, non-source asset, plan error) fail fast here.
- `createTask` — record the task, `pending` (queued, no `started_at`) or `running` (immediate).
- `execute` — scope guard → secret injection → container run → artifact capture → interpretation →
  finish. Shared by both paths.

`Enqueue(req)` = prepare → create pending task → hand to the pool → return the pending task. A worker
claims it, flips it `pending → running` (`StartTask`, stamping `started_at`), and calls `execute`.
`Run(ctx, req)` is retained as the **synchronous** path (prepare → create running → execute) for callers
that need in-line, ordered completion — notably the playbook runner's sequential steps.

**Task lifecycle.** A new `pending` status (already permitted by the tasks CHECK constraint) precedes
`running`. Terminal states are unchanged (`succeeded` / `failed`).

**Cancellation covers both phases.** `Cancel` kills a running task's container and cancels its context
as before; for a still-queued task it marks the id so the worker **skips** it and records it
`failed` / "cancelled by user" immediately — no wasted run.

**Crash reconciliation.** On startup the engine marks any task left `pending` or `running` from a prior
process (a crash mid-run) as `failed` ("interrupted"), so ghosts don't linger. (Full resume-after-restart
of queued work is deliberately out of scope; the queue is in-process.)

**API contract.** `POST /v1/tasks` → **202 Accepted** with the pending task. Clients poll
`GET /v1/tasks/{id}` until terminal, then read `GET /v1/tasks/{id}/artifacts` and
`/observations`. The Scan surface polls and shows a **Queued… / Running…** button state; the Home cockpit
lists queued tasks alongside running ones.

## Consequences

- **Non-blocking, disconnect-safe runs** with bounded host load.
- **Visible queue** — pending tasks show in the cockpit; the whole fleet of scheduled/triggered runs is
  observable, not hidden inside a blocked handler.
- **One execution body** (`execute`) shared by sync and async paths — no drift between them.
- **Breaking API change** — `POST /v1/tasks` no longer returns the full outcome synchronously; it returns
  the pending task (202). The desktop client was updated to poll.
- **Not yet async:** the playbook runner still executes its steps synchronously within its request (it
  needs ordered completion); moving whole playbooks onto the queue is a follow-up. The queue is
  in-process (not persisted), so queued-but-unstarted work does not resume across a restart — it is
  reconciled to failed instead.

Composes with ADR-0004 (the sandboxed runner is unchanged — only *when* it is invoked changed) and
ADR-0019 (scheduled/triggered playbook runs now queue behind the worker pool).
