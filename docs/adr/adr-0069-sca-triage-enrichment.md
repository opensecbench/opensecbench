# ADR-0069 — SCA triage enrichment: decision-axis attributes, SBOM linkage, remediation grouping

Status: Accepted. Dependency-CVE findings are enriched at interpret time with the attributes a triage
*decision* hangs on — package/version, fixed version, and (via the SBOM) direct-vs-transitive plus the
dependency path — and the Queue lets you group and sort by those axes (by dependency, by fix) without
discarding anything. This makes a pile of individually-real SCA CVEs tractable by organizing and
prioritizing rather than by ignoring severity, and gives the AI triage real signal instead of flagging
everything. It amends ADR-0028/0037 (richer observation attributes) and builds on ADR-0068 (queue-first).

## Context

Queue-first triage (ADR-0068) puts every observation in one Queue and makes reachability/severity filters,
not routers. A live test exposed the volume problem: scanning a Java web app produced ~124 real dependency
CVEs (osv-scanner 110 unique + grype 7 unique + 15 shared). The batch AI triage dismissed 5 and **flagged
124** — because for SCA, every CVE is genuinely "real," so a dismiss/flag pass cannot reduce the pile. The
operator was right that ignoring mediums is not acceptable (chained low-severity issues are often the best
findings) and that both scanners should stay (they are complementary, not redundant — exact CVE dedup
already works via ADR-0031/0037, so nothing is double-counted).

The root cause is that **SCA observations are attribute-poor**. Today a dependency-CVE observation carries
little more than severity, a CVE/GHSA alias, the tool name, and the coarse project-level `exposed` flag. It
lacks the axes a decision actually turns on:

- **which package/version** is affected (only 6 of 132 vuln observations even have a `package` attribute —
  the shared SARIF interpreter does not extract it);
- **is there a fix** (`fixed_version`) — the most actionable SCA question;
- **direct vs transitive**, and **what pulls the vulnerable dependency in** — a transitive CVE is fixed by
  upgrading its direct parent, not the leaf. The syft SBOM already carries this dependency graph, but the
  CVE findings and the SBOM are **not linked**;
- **reachability** — is the vulnerable symbol actually called (the JVM gap: govulncheck is Go-only).

Because the findings are attribute-poor, neither the operator nor the triage agent can prioritize —
grouping-by-package alone would only reorganize, and only for SCA. Cross-type attack chains (a SAST low + an
exposed route + a reachable dependency CVE) are correlation across the whole observation set, which is the
Analyst/investigation's job, not a static group-by. Enrichment is the foundation all of that needs.

## Decision

Enrich SCA findings with decision-axis attributes at interpret time, link them to the SBOM, and present
them grouped/sorted by those axes — **organize and prioritize, never discard**.

1. **Interpret-time enrichment (shared SARIF interpreter, grype + osv-scanner).** Extract onto each SCA
   observation, with consistent attribute names across tools so a merged observation stays coherent:
   - `package` (coordinate, e.g. `org.apache.commons:commons-text`) and `version`;
   - `fixed_version` (from the advisory's fix data; empty when no fix exists);
   - retain existing `security_severity` (CVSS) and the CVE/GHSA aliases.
   These are present in the grype/osv SARIF result (message / locations / properties); extraction is
   tool-shape-specific and validated against real tool output.

2. **SBOM linkage — direct/transitive + path.** Correlate each CVE's `package` against the latest syft
   SBOM's dependency graph to set:
   - `dependency` = `direct` | `transitive`;
   - `dependency_path` = the chain from a direct dependency to the vulnerable leaf
     (e.g. `spring-boot-starter-web → … → commons-text`).
   Best-effort: no SBOM or no match leaves the attributes absent — never blocks or drops a finding. This is
   what turns a transitive CVE into an actionable item (upgrade the direct parent).

3. **Reachability slots into the same axis set.** Enrichment leaves room for a `reachable` /
   `reachable_confirmed` verdict from a future JVM analyzer (tracked separately). Until then, `fixed_version`
   and direct/transitive carry prioritization where reachability can't. The ADR-0030–0034 reachability
   attributes are already part of this axis set.

4. **Presentation: group and sort by any axis; hide nothing (extends ADR-0068).** The Queue gains grouping
   and sorting over the enriched attributes. The default SCA view **groups by dependency** — e.g.
   `jackson-databind 2.13.1 → 6 CVEs (2 critical, 4 medium), fix: 2.13.4` — collapsible to every underlying
   CVE. Grouping is *collapse, not filter*: every finding, including mediums and lows, stays visible and
   expandable, and severity never removes anything. Chained-low analysis benefits — a dependency's or a
   route's related lows cluster instead of scattering across 124 rows.

5. **Remediation view.** A derived "fix plan" groups CVEs by the `fixed_version` upgrade that clears them:
   *"upgrading these M dependencies resolves N findings."* This is the most actionable SCA framing and falls
   out of the enrichment for free.

6. **Per-finding-type units.** SCA is the first and highest-volume case; the framework is per-type. SAST
   groups by rule/CWE (and its dataflow sink); secrets by detector/type. Only SCA grouping ships first.

7. **Chaining stays the agent's job.** Cross-type attack chains are correlated by the Analyst/investigation
   over the enriched observation set, not by a static group-by. Enrichment gives the agent the signal
   (reachability, exposure, fix, path) to find them.

## Consequences

- **Triage becomes tractable without discarding anything.** "Review 124 flat CVEs" becomes "review ~a dozen
  vulnerable dependencies / act on a fix plan," with every finding still present and expandable. Directly
  answers the operator's constraints: keep both scanners, never ignore mediums, surface chains.
- **The AI triage gets real signal.** With fix availability, direct/transitive, and (later) reachability,
  the triage agent can prioritize and cluster instead of flag-everything.
- **The SBOM stops being dead weight** — syft's dependency graph now drives triage.
- **Net-new:** the CVE→SBOM link and the remediation view. The interpreter enrichment is the foundation both
  (and grouping, sorting, and the agent's judgment) build on.
- **Amends** ADR-0028/0037 (richer observation attributes); **builds on** ADR-0068 (this is how the Queue
  stays tractable without routing/discarding) and ADR-0030–0034 (reachability enrichment is one axis).
- **Trade-offs / edges:** SARIF package/fix extraction differs by tool (grype vs osv shapes) — handle per
  tool, validate against real output; SBOM path resolution is heuristic (version ranges, multiple paths —
  report the shortest and note when several exist); non-SCA grouping and JVM reachability are follow-ons.

**Build phases:**
1. **Interpreter enrichment** — `package`, `version`, `fixed_version` on grype/osv observations (validate
   against real tool output).
2. **SBOM linkage** — `dependency` (direct/transitive) + `dependency_path` from the syft SBOM.
3. **Queue grouping + sort + remediation view** (UI over the enriched data).
4. **SCA-aware triage agent** — use the axes to prioritize/cluster instead of flag-all.
   (JVM reachability tracked separately; feeds the same axis set when it lands.)
