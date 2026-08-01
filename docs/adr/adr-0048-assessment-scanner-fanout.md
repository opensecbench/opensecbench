# ADR-0048 — Fan out the assessment scanners

Status: Accepted — delivered. The autonomous `assessment` playbook's single `scan` step is split into four
parallel per-scanner steps, so the scan phase runs the scanners concurrently instead of one sub-agent running
them in sequence. This is what makes ADR-0046 (parallel plan steps) pay off in the flagship flow.

## Context

ADR-0046 gave the plan runner wave scheduling — independent steps run concurrently. But the built-in
`assessment` playbook had a single `scan` step: one `code-analysis` sub-agent instructed to run opengrep,
grype, govulncheck, and trufflehog in turn. Those scanners are independent (different tools, mostly different
inputs), yet they ran serially inside one loop, so the scan phase took the *sum* of the four scanners' times —
and the new parallel scheduler had nothing to parallelize. The onboarding playbook already fanned out (two
steps on `surface`); the assessment, the most scan-heavy playbook, did not.

## Decision

Split `scan` into four sibling steps, each depending only on `recon` and each running one scanner via
`run_capability`:

- `scan-sast` — opengrep (SAST + dataflow reachability)
- `scan-sca-grype` — grype (dependency/SCA)
- `scan-sca-govulncheck` — govulncheck (Go call-graph reachability)
- `scan-secrets` — trufflehog (secrets)

`triage` now depends on all four. Because they share only the `recon` dependency, the plan runner schedules
them as one concurrent wave (bounded by `maxParallelSteps`=4, which exactly fits) — the scan phase now takes
the slowest scanner's time, not the sum. Each step notes what it ran to `analysis/scan-<tool>.md`; the
platform still routes and enriches every tool's results into the observation queue automatically, so triage is
unchanged. The approval gate and propose-only discipline (ADR-0044/0035) are untouched.

## Consequences

- **The scan phase parallelizes.** A real assessment overlaps its four Docker-backed scanners instead of
  serializing them — the single biggest wall-clock win in an autonomous run, and the concrete payoff for the
  parallel scheduler.
- **Finer-grained, resumable progress.** Each scanner is its own step, so a poll of the plan shows which
  scanners have finished, and a failed/skipped scanner (e.g. govulncheck with no Go assets) skips only itself
  — the others and triage still proceed on whatever ran.
- **Exercised by a test.** A unit test drives the real assessment playbook and asserts the four scanners run
  concurrently (peak in-flight ≥ 2) and the plan then pauses at the approval gate — tying the playbook shape
  to the scheduler.

## Out of scope — later
Per-asset scanner fan-out (one step per repo × tool) for wider parallelism on multi-repo projects; a
`maxParallelSteps` raise if hosts can take more concurrent containers; conditional steps (skip a scanner step
entirely when the KB shows no relevant assets, rather than the sub-agent no-op'ing); fanning out the other
playbooks similarly.

Composes with ADR-0046 (the parallel scheduler this exploits), ADR-0035 (the autonomous assessment), and
ADR-0044 (the approval gate that still follows triage).
