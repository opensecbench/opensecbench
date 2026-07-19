# ADR-0032 — SAST dataflow reachability (reachability Phase 2b)

Status: Accepted — delivered. A semgrep security finding is reachability-gated the same way SCA findings
are (ADR-0030/0031): a **taint** finding carries a SARIF `codeFlows` dataflow trace (untrusted source →
vulnerable sink) — semgrep's engine proving the code is reachable from input — so such a finding on an
**exposed service** escalates, while a plain pattern match routes on severity.

## Context

Reachability gating so far covered SCA: govulncheck's call graph for Go (ADR-0030), reused across tools by
CVE (ADR-0031). SAST was left routing on severity alone, flooding triage with pattern matches that may never
be reached from an entry point. Real interprocedural call-graph reachability for arbitrary languages is
research-grade, but semgrep already gives a deterministic, machine-readable signal we were discarding:
**taint-mode rules emit a `codeFlows` trace in SARIF** — the dataflow path from an untrusted source to the
vulnerable sink. Presence of that trace is the SAST analog of govulncheck's symbol-level trace: the tool
proved the finding is reachable from input, not just that the pattern exists.

## Decision

**Read the dataflow trace from SARIF.** The SARIF interpreter now inspects `result.codeFlows`. When a
finding has one (a taint/dataflow finding), it sets `reachable=true` and captures the trace's origin as
`dataflow_source` (`file:line` of the first thread-flow location — where untrusted input enters). A plain
pattern finding has no codeFlow and gets no `reachable` attribute (reachability is not applicable, not
"false"). This only affects semgrep — grype's SCA SARIF has no codeFlows, and its reachability still comes
from the govulncheck correlation (ADR-0031), so the shared interpreter needs no per-tool branch.

**semgrep routing gates on it.** semgrep's manifest now declares `sastReachabilityRouting`, in order:
1. `{reachable:true, exposed:true} → investigate` — a dataflow-reachable finding on an exposed service, at
   any severity (dataflow reachability is a strong signal).
2. `{MinSeverity:high} → investigate` — fallback: a high/critical pattern finding (e.g. hardcoded
   credentials) still investigates even without a dataflow trace.

So a taint-proven injection on an exposed service escalates even at medium severity; a plain high-severity
finding still escalates on severity; a low/medium pattern match stays in review. There is no
`reachable:false` downgrade for SAST — absence of a trace means "not a dataflow finding", not "unreachable".

## Consequences

- **SAST triage focuses on exploitable dataflow.** Source→sink findings on exposed services rise to the top;
  pattern noise stays on severity.
- **Real + free.** The signal is semgrep's own taint analysis, already in the SARIF we parse — no new tool,
  no call-graph engine, and the `reachable` attribute + triage pills from ADR-0030 light up for SAST too.
- **`dataflow_source` aids validation.** The investigation/agent sees where untrusted input enters, not just
  the sink.
- **Depends on taint rules running.** `--config auto` includes taint-mode security rules; a scan with only
  pattern rules yields no codeFlows and degrades to severity routing (no false downgrade).
- **Exposure is still project-level.** As with SCA, `exposed` says the service is on the network, not that
  this specific handler is the exposed one. Mapping a dataflow source to a specific exposed HTTP route
  (framework-aware entry-point resolution) remains future work.

## Out of scope — later
- Framework-aware entry-point resolution (which exposed route reaches this source); interprocedural
  call-graph reachability beyond semgrep's taint engine; retroactive re-evaluation and cross-tool merge
  (shared with ADR-0031 Phase 2b).

Composes with ADR-0029 (SARIF attributes/fingerprint), ADR-0030 (`exposed` enrichment + `reachable`
routing + pills), and ADR-0031 (reachability as a routed attribute).
