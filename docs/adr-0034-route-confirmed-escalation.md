# ADR-0034 — Route-confirmed escalation

Status: Accepted — delivered. The exposed-route association from ADR-0033 becomes a routing **gate**: a
finding sitting in a **traffic-confirmed** exposed route's handler escalates to an investigation at medium
severity or above — being directly on a live endpoint is strong exposure evidence even without a
reachability proof.

## Context

ADR-0033 tied a finding to a specific exposed route (`exposed_route` / `route_observed` attributes) but used
it only as triage context — routing still gated on reachability + the coarse project-level `exposed`. So a
medium-severity finding sitting directly in a handler we literally saw serve traffic could fall to manual
review, while reachability alone (which SAST/SCA often can't prove) was the only escalation path.

## Decision

Add a **route-confirmed** rule to the SAST (semgrep) and SCA (grype) routing lists:
`{when: {route_observed: "true"}, min_severity: "medium"} → investigate`. A finding whose handler file
declares a route that captured traffic confirmed exposed, rated medium or higher, opens an investigation —
regardless of whether reachability was provable.

Two guardrails:
- **Escalate only, never downgrade on absence.** Route detection is heuristic and incomplete (a starter
  ruleset, file-level association), so "no route found" is not evidence of safety and must never move a
  finding *down*. The rule only adds escalation; findings with no route keep their prior routing.
- **Authoritative reachability still wins (grype).** The rule is ordered *after* grype's
  `{reachable: "false"} → review`: if govulncheck proved the vulnerable symbol is never called, sitting in a
  live handler's file doesn't make it exploitable, so it stays in review. It sits *before* the
  reachable+exposed and severity-fallback rules.

Only `route_observed` (traffic-confirmed) drives escalation, not a merely-declared route — a route we never
saw hit is weaker evidence and continues to route on reachability/severity.

## Consequences

- **Live-endpoint findings surface.** A medium+ finding directly on an exposed, exercised route is
  investigated even when neither semgrep's taint engine nor govulncheck could prove reachability — closing
  the common gap where reachability is simply unknown.
- **No new false negatives.** Because absence of a route never downgrades, incomplete route coverage can't
  hide findings; the worst case is a finding that isn't *extra*-escalated.
- **Consistent precedence.** grype's proved-uncalled downgrade remains authoritative; semgrep (no
  authoritative `reachable:false`) escalates on the route first.

## Out of scope — later
Escalating on a merely-declared (not traffic-confirmed) route; call-graph route→sink reachability (still
file-level); applying the gate to govulncheck (its reachability is already authoritative).

Composes with ADR-0033 (`route_observed`), ADR-0031/0032 (the reachability rules this orders around), and
ADR-0028 (the disposition matcher).
