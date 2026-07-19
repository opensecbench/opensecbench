# ADR-0030 — Static reachability + exposed-service model

Status: Accepted — Phase 1 delivered. For dependency vulnerabilities, "is the vulnerable code actually
**reachable** in the call graph, and is the service **exposed**?" decides whether a finding is escalated.
Phase 1 adds real Go call-graph reachability (govulncheck), a derived exposed-service signal, and routes
`reachable + exposed → investigate`. SAST reachability and cross-tool correlation are Phase 2.

## Context

ADR-0028/0029 route SCA/SAST findings on severity alone — a grype high/critical opens an investigation
whether or not the vulnerable symbol is ever called, or the service is on the network. That floods triage
with vulns that are imported-but-unreachable or sit in an internal tool. James wants a **reachability
analysis** for SCA/SAST findings, escalating only those that are reachable **and** part of an exposed
service. He chose **real static call-graph** analysis over an agent estimate.

The data reality (surveyed): there was near-zero static reachability data — a flat syft component→dependsOn
graph parsed on the fly, no call sites, no CVE↔component correlation, and no first-class "exposed" signal
(exposure was only inferable, project-level: nmap open-ports, proxy exchanges, `cloud_deployment`/
`infrastructure` assets, with no join to a finding).

## Decision

Reachability and exposure are modelled as **observation attributes** the existing disposition layer routes
on (ADR-0028) — no new routing mechanism, and dedup (ADR-0029) keeps the analysis from re-running every scan.

**Reachability via govulncheck (real call graph).** A new `govulncheck` capability runs Go's official
vulnerability analyzer, which builds the call graph and reports whether each vulnerable **symbol** is
actually called. `pkg/interpret/govulncheck.go` parses its JSON stream into one observation per OSV/CVE and
sets `reachable=true` when any finding for that vuln has a symbol-level trace (the function is called),
`reachable=false` when the vuln is only imported/required. Attributes also carry `tool=govulncheck`,
`osv`, `package`, and `fixed_version`. This is deterministic call-graph reachability, not an estimate.

**Exposed-service signal (derived).** `store.ProjectExposure(project)` computes whether the project hosts an
exposed service from existing evidence — nmap `nmap/open-port` observations, captured proxy exchanges, and
`cloud_deployment`/`infrastructure` assets — returning a boolean + a compact surface summary (ports/
endpoints/deployments). At disposition time the engine enriches each new observation's attributes with
`exposed=true|false` (a scan-time snapshot; cheap, computed once per task). No new manual data entry.

**Reachability-gated routing.** The govulncheck manifest declares
`{when:{reachable:"true", exposed:"true"} → investigate}`; everything else (not reachable, or reachable but
not exposed) falls to manual **review**. So an exposed, reachable dependency vuln opens a tracked
investigation automatically; an imported-but-uncalled vuln, or one in an internal-only service, does not
flood triage. Projects override via `disposition_rules` as usual.

## Consequences

- **Triage sees what matters.** Reachable vulns on exposed services escalate; unreachable/internal ones stay
  quiet — the core of James's request.
- **`exposed` is a generic attribute.** Any tool's rules (and project overrides) can now gate on exposure,
  not just reachability.
- **Go-only reachability in Phase 1.** govulncheck is Go-specific; grype's multi-language vulns still route
  on severity until a reachability analyzer exists for their ecosystem. Correlating govulncheck reachability
  onto grype observations by CVE is Phase 2.
- **Snapshot exposure.** `exposed` is captured at scan time and, like other attributes, not refreshed on a
  deduped re-scan (ADR-0029 limitation) — a service that becomes exposed later needs a fresh (non-duplicate)
  finding to re-evaluate; a periodic re-derivation is future work.
- **govulncheck is heavy.** It installs the analyzer and downloads the vuln DB + module graph per run
  (~minutes); a prebuilt image is a future optimization.

## Out of scope — Phase 2 (later)
- **SAST reachability**: entry-point (exposed HTTP handler) → flagged-sink reachability for semgrep findings,
  via a call graph / semgrep dataflow. Harder and per-language; the exposure model + `exposed` attribute +
  routing built here are the shared foundation.
- **Cross-tool correlation**: map govulncheck reachability verdicts onto grype CVE observations (and parse
  the syft SBOM into tables for the CVE↔component join).
- **Explicit exposure**: let an application be marked exposed/internal manually, and join an exposed host to
  a specific finding's component; periodic re-derivation of the snapshot.
- Multi-language reachability analyzers beyond Go.

Composes with ADR-0028 (disposition routing — reachability/exposure are just attributes), ADR-0029
(fingerprint dedup + SARIF attributes), and ADR-0005 (observations→findings).
