# ADR-0046 — Parallel plan steps

Status: Accepted — delivered. The plan runner executes **all currently-ready steps concurrently** in each
scheduling wave, instead of one at a time — so independent steps (e.g. the SAST / SCA / secrets scanners)
overlap. The last of ADR-0035's remaining autonomy items (after mid-run approval and report generation).

## Context

`runPlan` (ADR-0019, made resume-safe + gate-aware in ADR-0044) walked the step-DAG and delegated **one ready
step at a time**. When a playbook fanned out — the assessment `scan` step conceptually runs opengrep, grype,
govulncheck, and trufflehog, and a DAG could model them as siblings — they still executed serially, so a run
took the *sum* of the branch times even though the branches were independent. The DAG already encoded which
steps were independent; the runner just wasn't using it.

## Decision

**Wave scheduling.** Each pass, `runPlan` collects the whole ready set (all steps whose dependencies are
satisfied) and runs them **concurrently** as one wave via `runWave`, blocking until the wave finishes before
re-evaluating. Wall-clock for a fan-out becomes the *slowest* branch, not the sum.

- **Bounded concurrency.** A `maxParallelSteps` (4) semaphore caps in-flight delegations — steps often run
  Docker-heavy capabilities, so a wider ready set just takes more waves rather than swamping the host.
- **Shared state under a mutex.** The `done`/`failed`/`results` maps are folded from each goroutine under a
  lock; a step's dependency context (`planContext`) is read under the same lock before delegating.
  Dependencies come from *earlier* waves, so their results are stable while the current wave runs. Verified
  clean under `go test -race`.
- **Gates and failures unchanged.** An uncleared gate still parks the whole plan **before** a wave starts
  (nothing runs that a human might veto); approved gates resolve inline; a failed/skipped dependency still
  skips its dependents. The scheduling refactor preserves ADR-0044's pause/resume semantics exactly.

## Consequences

- **Faster real runs.** Independent analysis (multiple scanners, multiple repos, per-asset recon) overlaps,
  so an assessment finishes in roughly its critical-path time instead of its total-work time.
- **Same correctness, proven concurrent.** Existing happy-path / cycle / gate tests still pass; a new fan-out
  test (`root → {a,b,c} → join`) asserts the three middle steps are actually in flight together (peak
  concurrency ≥ 2) and that the join still receives every branch's result.
- **A modest, safe cap.** Four concurrent Docker-heavy steps is conservative; the value is mostly in
  overlapping the handful of independent branches a real playbook has, not in massive fan-out.

## Out of scope — later
A **pipelined** scheduler (start a step the moment *its* deps are done, rather than waiting for the whole wave
— removes the barrier's slowest-in-wave stall); a configurable / per-playbook concurrency cap; cancelling
in-flight steps when a sibling fails; **deeper delegation** (a sub-agent delegating further) and raising the
8-step sub-agent cap — the remaining threads noted under ADR-0035.

Composes with ADR-0019 (the plan runner), ADR-0044 (the pause/resume + gate semantics this preserves), and
ADR-0035 (the autonomous assessment whose scanner fan-out this speeds up).
