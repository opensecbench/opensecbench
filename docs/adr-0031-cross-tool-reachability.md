# ADR-0031 — Cross-tool reachability correlation (reachability Phase 2a)

Status: Accepted — delivered. A reachability verdict is a project-level fact about a **CVE**, independent of
which tool found it. govulncheck (ADR-0030) proves reachability for Go; this records those verdicts and
reuses them to make **any** SCA tool's CVE findings reachability-aware — so grype no longer floods triage
with high-severity CVEs that govulncheck already proved unreachable.

## Context

ADR-0030 gave real call-graph reachability, but only govulncheck's own observations carried it. grype covers
every ecosystem yet has no reachability, so its findings still routed on severity alone. For a Go project
running both tools, the same CVE appears from each, and grype would escalate a high-severity CVE to an
investigation even when govulncheck proved the vulnerable symbol is never called. Reachability needs to be a
shared, tool-agnostic signal.

## Decision

**A project reachability store, keyed by CVE.** `reachability(project_id, cve, package, reachable, source)`
UNIQUE per `(project_id, cve)` (migration 0039). `store.SetReachability` upserts a verdict;
`store.ReachabilityForCVE` reads one.

**Generic populate + enrich in the engine** (in the post-interpret create loop, for CVE observations —
`rule_id` starting `CVE-`):
- If the observation already carries a `reachable` attribute (an analyzer's own verdict, i.e. govulncheck),
  **record** it via `SetReachability` for other tools to reuse. No tool-specific code — it keys off the
  attribute.
- Otherwise, **enrich** the observation's `reachable` attribute from a stored verdict for that CVE
  (`ReachabilityForCVE`). This is how a grype CVE inherits govulncheck's call-graph result. The enrichment is
  persisted on the observation (and shown in triage); the content fingerprint excludes attributes, so dedup
  is unaffected.

**grype routing gates on reachability.** grype's manifest now declares, in order:
1. `{reachable:false} → review` — govulncheck proved it uncalled; don't escalate even if high severity.
2. `{reachable:true, exposed:true} → investigate` — reachable on an exposed service.
3. `{MinSeverity:high} → investigate` — fallback for CVEs with no reachability verdict (non-Go ecosystems).

So a grype CVE that govulncheck marked unreachable is downgraded; a reachable one on an exposed service
escalates; a CVE with no verdict still routes on severity. govulncheck keeps its own
`reachable+exposed → investigate` rule (reachable-but-internal stays review); semgrep is unchanged (its
rule ids aren't CVEs, so nothing correlates — SAST reachability is Phase 2b).

## Consequences

- **Reachability is shared.** govulncheck's Go call-graph result now steers grype's Go findings; the store is
  the seam for adding more per-ecosystem analyzers later (each just records CVE verdicts).
- **Triage focuses.** Known-unreachable CVEs drop to review; only reachable-and-exposed ones auto-escalate.
- **Run order matters (v1).** Enrichment reads whatever verdicts exist when an observation is created, so
  govulncheck should run before grype to steer it; a grype finding created earlier is not retroactively
  re-routed when govulncheck later runs (ADR-0029 dedup won't recreate it). Retroactive re-evaluation on new
  evidence is Phase 2b.
- **Cross-tool duplicate observations remain.** grype and govulncheck still each record an observation for
  the same Go CVE (different evidence/locations); the grype one is now correctly routed, but merging the two
  is future work.

## Out of scope — later
- **Phase 2b**: retroactive re-evaluation of existing findings when a new reachability verdict arrives;
  cross-tool observation merge for the same CVE; SAST reachability (exposed handler → sink).
- Multi-language reachability analyzers; correlating by package/version when a CVE id is absent.

Composes with ADR-0030 (govulncheck verdicts + `exposed`), ADR-0029 (fingerprint dedup, unaffected), and
ADR-0028 (the disposition matcher — reachability is just attributes it routes on).
