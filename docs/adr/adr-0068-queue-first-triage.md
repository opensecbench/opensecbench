# ADR-0068 — Queue-first triage: reachability is a filter, not a router

Status: Accepted. Scanners stop auto-opening investigations. Every interpreted observation lands in one
review **Queue**; reachability, exposure, route, and severity become **decision-support filters** on that
Queue rather than rules that pre-sort findings into a "Validating" bucket. Investigations become deliberate
— opened by a human or an agent pointed at a Queue item. The lone surviving auto-route is a live-verified
secret → finding. This supersedes the auto-investigate stance of ADR-0028 and the reachability-routing arc
(ADR-0030/0031/0032/0033/0034), whose *enrichment* is retained as the filter signal.

## Context

ADR-0028 introduced tool-declared disposition rules that auto-route interpreted observations to
finding / investigate / review, and the reachability arc (ADR-0030–0034) layered severity- and
reachability-based **auto-investigate** rules on top: `severity ≥ high → investigate`,
`reachable+exposed → investigate`, `route_observed → investigate`, etc. The intent was to pre-triage — lift
the important signals into a working "Validating" surface so they aren't lost among the noise.

In practice this backfired:

- **The bucket looks authoritative but isn't.** Routing rules are heuristic and imperfect, so the
  auto-produced Validating set is imperfect. We watched a *critical* (Text4Shell) sit in the Queue while
  lesser findings were in Validating — because osv-scanner reported it first at a lower severity and a
  later grype merge upgraded it without re-routing. Any bucket the rules produce can be wrong, so **no
  bucket is safe to skip.**
- **It invites tunnel vision.** If a human — or a naively-scoped agent — treats Validating as *the*
  worklist, whatever the rules left in the Queue is silently neglected. That is the opposite of the
  "don't miss anything" guarantee triage is supposed to provide.
- **"High severity" is not a question.** An investigation should mean *"there is a specific uncertain
  thing to confirm"* (is this secret live? is this path reachable?). A high-severity CVE with no open
  question isn't something to *validate* — it's something to **review and decide on**, which is the
  Queue's job. Auto-`severity≥high → investigate` inflated Validating with items that had no open
  question, burying the few that did.

## Decision

1. **Scanners declare no auto-investigate dispositions.** grype, osv-scanner, opengrep, semgrep,
   govulncheck, and route-map ship with empty `Dispositions`; their interpreted observations land in the
   Queue as `unreviewed`. The shared routing vars (`reachableExposed`, `sastReachabilityRouting`,
   `scaReachabilityRouting`) are removed.

2. **Reachability/exposure/route/CVSS stay — as filters, not routers.** The enrichment of
   ADR-0030/0031/0032/0033/0034 still runs (`correlateReachability`, `correlateExposedRoute`, exposure
   derivation, `ReEvaluate`), tagging observations with `reachable`, `reachable_confirmed`,
   `route_reachable`, `route_observed`, `exposed`, `security_severity`. These surface as pills and
   **filter/sort controls in the Queue**, so an operator can work reachable-and-exposed criticals first —
   without anything being auto-sorted out of view. `ReEvaluate` continues to enrich retroactively (a route
   or reachability fact arriving later updates the observation's filter signals) but no longer escalates.

3. **The one surviving auto-route: a live-verified secret → finding.** A trufflehog `verified` hit was
   live-checked against its provider, so it is a confirmed finding with no open question. Everything else
   — including unverified secrets — lands in the Queue.

4. **Investigations become deliberate.** A human, or an agent explicitly pointed at a Queue item, opens an
   investigation when there is a real question to work. The batch-triage agent already operates over the
   **whole** untriaged Queue, so the dependable triage motion is "work the Queue to completion."

5. **Project overrides remain.** A team that wants auto-routing back can add project-level disposition
   rules (`disposition_rules`); the default is simply queue-first.

## Consequences

- **One pile, one motion.** Triage is "work the Queue"; nothing hides behind an imperfect auto-bucket.
  Findings and (deliberate) investigations are the only things that leave the Queue.
- **Reachability keeps its value** as the prioritization lens it was always best at — the arc's analysis
  (call-graph reachability, dataflow traces, route mapping, exposure) is retained; only its role as an
  auto-router is dropped.
- **Reverts the merge re-disposition patch.** That change existed to make a merged severity-upgrade
  re-route; with severity no longer routing anything, it is unneeded complexity and is removed.
- **Amends ADR-0028; supersedes the auto-investigate rules of ADR-0030/0031/0032/0033/0034** (their
  enrichment model stands). ADR-0034's escalate-only-never-downgrade principle is moot once nothing
  auto-routes.
- **Requires Queue filters.** The value of "reachability as a filter" depends on the Queue exposing
  reachable/exposed/severity as sort/filter controls; surfacing those is the frontend follow-on.
- **Trade-off:** no automatic prioritization bucket out of the box. Accepted — a wrong bucket is worse
  than none, and filters put the operator in control without hiding anything.
