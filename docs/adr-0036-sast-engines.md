# ADR-0036 — Pluggable SAST engines (opengrep default; Semgrep-Pro; Checkmarx)

Status: Accepted — opengrep default + Semgrep-Pro delivered; Checkmarx planned. SAST is an engine family
sharing the SARIF → observation → reachability pipeline (ADR-0029/0032). **opengrep** is the open default and
the one that makes dataflow reachability actually work; **Semgrep** stays available for existing customers
(Pro engine via license); **Checkmarx** is planned for licensed users.

## Context

Real-tool validation (ADR-0032) found that **Semgrep CE masks SARIF `codeFlows`** (and metavars/lines/
fingerprints) behind a commercial login, so SAST dataflow reachability was inert on the open tool. James
wanted the free path to work, plus support for teams that already pay for Semgrep, and — eventually —
Checkmarx. That makes SAST a small **family of interchangeable engines**, all emitting SARIF that the
existing interpreter turns into observations with `reachable`/`security_severity`.

## Decision

**opengrep is the default SAST engine.** opengrep is the LGPL-2.1 fork of Semgrep 1.100.0 created (Jan 2025)
precisely because Semgrep moved taint/interprocedural analysis + codeFlows behind login. It is CLI/rule/SARIF
compatible. The `opengrep` capability runs `opengrep scan --sarif --dataflow-traces --config auto` — and
`--dataflow-traces` is **required**: it is what emits the SARIF `codeFlows` the reachability interpreter
reads. OSB ships the engine as **`osb/opengrep`** (`images/opengrep`, pinned binary; the second OSB-built
image after claude-cli). `route-map` also moved to this image (it un-masks metavars as a bonus).

**Semgrep stays, with license support.** The `semgrep` capability keeps `--dataflow-traces` and gains a
`pro=true` param that adds `--pro`; combined with a `SEMGREP_APP_TOKEN` secret ref (injected as env at exec,
ADR-0011) it enables the Pro engine so an existing customer gets interprocedural taint + codeFlows. Without a
license it is plain CE (no codeFlows) — prefer opengrep.

**Checkmarx is planned (licensed).** A `checkmarx` capability will wrap the Checkmarx One CLI (`cx`),
credential-gated (tenant URL + API key as vault secrets), emitting SARIF into the same pipeline. Deferred:
it needs a Checkmarx tenant to build/test, which we don't have — tracked in TODO.

All engines share `sastReachabilityRouting` and the SARIF interpreter, so reachability/exposure/route
gating (ADR-0032/0033/0034) applies uniformly regardless of engine.

## Consequences

- **SAST reachability works for free.** Validated end-to-end in Docker: real opengrep + `--config auto`
  flagged the sample SQLi and marked it `reachable=true` with `dataflow_source` at the taint origin, through
  the unchanged interpreter. This supersedes ADR-0032's "needs Semgrep Pro" limitation for the default path.
- **Existing Semgrep customers aren't left out** — one param + a token secret and their Pro engine drives the
  same pipeline (implemented per Semgrep docs; unverified here without a license).
- **Engine choice is per-run / per-playbook.** The `assessment` playbook now runs `opengrep`. Nothing else in
  the reachability/exposure stack changes — engines are swappable behind SARIF.

## Real-tool validation (2026-07-19, Docker)
opengrep 1.25.0: `--dataflow-traces` emits SARIF `codeFlows` + JSON `dataflow_trace` + metavars (all masked in
Semgrep CE); the interpreter parsed them to `reachable=true` + `dataflow_source`. Two image fixes: the binary
is a glibc/manylinux standalone (base `debian:12-slim` + ca-certificates + git), and its Python runtime reads
files with the locale encoding — debian-slim is ASCII, so a UTF-8 byte in a ruleset/source crashed it;
fixed with `PYTHONUTF8=1` + `LANG/LC_ALL=C.UTF-8`. route-map verified on the opengrep image (5 routes).

## Out of scope — later
Build the Checkmarx capability (needs a tenant); other engines (SonarQube, etc.); per-project default-engine
setting; verifying the Semgrep-Pro path against a real license; arm64 opengrep image variant.

Composes with ADR-0032 (the codeFlows reachability signal this finally activates), ADR-0011 (secret-ref
injection for the Semgrep license), and ADR-0018 (the OSB-built-image convention).
