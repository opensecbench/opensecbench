# ADR-0037 — Observation & investigation dedup polish

Status: Accepted — delivered. Three fixes to the observation/investigation pipeline surfaced by live
end-to-end testing (ADR-0029–0036): a project-observations API, refresh-on-rescan, and cross-tool
vulnerability de-duplication.

## Context

Running the whole pipeline against a real repo exposed three rough edges:
1. **No HTTP endpoint returned a project's observations** — only the agent tool and the graph view did, so
   triage UIs/tooling couldn't list them.
2. **A re-scan never updated a deduped observation** — the content fingerprint (ADR-0029) skips a re-seen
   finding entirely, so a corrected interpreter (e.g. the severity fix) or a changed reachability/exposure
   signal left stale data on the existing observation.
3. **The same CVE from two tools opened two investigations** — grype reports a vuln by GHSA, govulncheck by
   CVE (+GHSA); the disposition layer routed each to its own investigation, double-counting one vuln.

## Decision

**Project observations endpoint.** `GET /v1/projects/{id}/observations` (`?unreviewed_only=true`) →
`store.ListObservationsByProject`, with a client method and `osb observation list --project [--unreviewed]`
(which also prints the reachable/exposed_route/CVSS signals).

**Refresh-on-rescan.** When the engine dedups a re-seen observation by fingerprint, instead of skipping it
outright it now `RefreshObservation`s the interpreter-derived fields — severity, detail, attributes — while
**preserving `review_state`** and **not** re-running dispositions. So a fixed severity or a changed signal
lands on the existing finding without disturbing human triage or re-firing investigations/findings.
(Enrichment now runs before the dedup check so the refresh carries the current exposure/route/reachability.)

**Cross-tool vulnerability dedup.** A new `investigation_vulns` table maps each advisory id a project is
investigating to its investigation, `UNIQUE(project_id, vuln_id)`. Before opening an investigation for a
vuln observation, the engine checks `InvestigationForVuln` for any of the observation's advisory ids
(`vulnIDs` → CVE + GHSA); if the vuln is already tracked, it skips the duplicate. The first tool to find a
vuln owns its investigation; a second tool reporting the same vuln under a different id scheme is deduped.

## Consequences

- **One vuln → one investigation**, regardless of how many tools report it — triage stops double-counting.
  Both observations still exist (distinct evidence: grype's manifest location vs govulncheck's call site),
  but only one investigation.
- **Re-scans keep findings current** without losing human triage or spawning duplicate work — the main
  friction from the ADR-0029 "skip on dedup" behavior.
- **Observations are queryable** over HTTP for any UI/tool.
- Non-vuln observations (SAST findings with no CVE/GHSA) are unaffected by the investigation dedup — they
  route as before.

## Out of scope — later
Cross-tool *observation* merge (both tools' observations remain, only investigations dedup); re-routing a
refreshed observation whose severity newly crosses a threshold (refresh updates data but does not
retroactively escalate); paginating the observations endpoint.

Composes with ADR-0029 (fingerprint dedup — refresh replaces the bare skip), ADR-0031 (`vulnIDs` CVE/GHSA
extraction, reused here), and ADR-0028 (investigations).
