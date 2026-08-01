# ADR-0029 — Observation dedup + multi-tool disposition routing

Status: Accepted — delivered. Observations gain a content **fingerprint** so a re-scan does not re-create or
re-dispose a finding it has already seen (no duplicate observations, no repeated investigations/token spend).
The shared SARIF interpreter now carries structured attributes, and **semgrep** (SAST) and **grype** (SCA)
declare dispositions — high/critical → a tracked investigation.

## Context

ADR-0028 made post-run routing generic, but left two gaps. First, **nothing dedups observations**: every
capability run minted a fresh observation UUID with a plain INSERT (`store.CreateObservation`,
`engine` interpret loop), so re-running a scan duplicated every finding — and with routing in place, each
duplicate re-fired its disposition, re-opening an investigation and re-seeding a `vuln-validator` thread.
The same finding would burn LLM tokens on every scan. Second, only TruffleHog declared rules. semgrep and
grype both flow through one SARIF interpreter that read four fields and set **no attributes** — it even
dropped `security-severity`/CVSS — so their observations could only be matched on a coarse
error/warning/note severity.

## Decision

**Content fingerprint (dedup).** Observations gain `fingerprint` (`sha256(origin|rule|location|detail)`,
`interpret.Fingerprint`). Before creating an observation the engine resolves the task's project once, sets
`project_id` (the ADR-0027 direct path) + `fingerprint`, and skips the observation entirely when
`store.ObservationByFingerprint(project, fp)` already matches — no create, no disposition. The hash excludes
`severity` and `attributes`, so a finding whose CVSS or `verified` flag shifts between runs is still the same
finding (dedup over refresh). Known limitations: a line shift or a changed message is treated as a new
finding; a deduped observation is not refreshed. Observations with no project (no task project/application)
skip dedup and behave exactly as before.

**SARIF attributes + CVSS-refined severity.** The interpreter now reads `tool.driver.name` → attribute
`tool`, and `security-severity` (from the result or, for semgrep, the rule definition) → attribute
`security_severity`. When a security-severity is present, severity is derived from the CVSS band
(≥9 critical, ≥7 high, ≥4 medium, >0 low), because the SARIF `level` collapses Critical and High to
`error` — so `MinSeverity` routing can distinguish them. The nmap and TruffleHog paths are untouched.

**semgrep + grype dispositions.** Both built-in manifests declare
`{MinSeverity: "high", Action: investigate}` (shared `investigateHighSeverity`). High/critical SAST & SCA
findings open a tracked investigation (human-triggered, findings stay human-gated per ADR-0019); lower
severities fall to manual review. No auto-finding — unlike a verified secret, SAST/SCA carry false
positives, so the agent + human validate first. Projects still override via `disposition_rules`. syft stays
raw (SBOM, no observations).

## Consequences

- **Re-scans are idempotent + cheap.** The same finding is recorded and dispositioned once; repeat scans add
  nothing and spend no tokens. This is what makes the ADR-0028 routing safe to leave on.
- **SAST/SCA are now routable.** grype criticals separate from highs; both tools' high/critical findings
  become investigations automatically.
- **Fingerprint is coarse (v1).** Line/message drift or a deduped observation's changed attributes are not
  yet reconciled — noted for a future fuzzy/last-seen pass.
- **Foundation for reachability (ADR-0030).** Reachability + exposure become additional attributes the same
  routing consumes; dedup keeps a reachability analysis from re-running every scan.

## Out of scope (later)
Fuzzy/line-tolerant fingerprints; refreshing a deduped observation's severity/attributes on re-scan; static
reachability and the exposed-service model (ADR-0030); an SCA-specific (non-SARIF) grype interpreter with
package/fixed-version detail.

Composes with ADR-0005 (observations→findings), ADR-0027 (`observations.project_id`), and ADR-0028
(disposition routing — this adds dedup and two more tool declarations, no mechanism change).
