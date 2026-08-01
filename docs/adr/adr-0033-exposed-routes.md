# ADR-0033 — Exposed route inventory + route-aware findings

Status: Accepted — delivered. Declared HTTP routes are extracted from source (a `route-map` capability),
confirmed against captured proxy traffic, and a finding whose handler file declares an exposed route is tied
to that entry point via an `exposed_route` attribute — refining the coarse project-level `exposed` signal
(ADR-0030) to a specific route.

## Context

Reachability gating answered "is the vulnerable code reachable?" but "part of an **exposed service**" was a
project-level boolean. James wanted to figure out the *exposed route* — tie a SAST/SCA finding to a specific
HTTP entry point — and chose the fullest approach: static route extraction **and** traffic confirmation with
a file-proximity link. He also noted that some assessments have little to go on, so the design must degrade
gracefully rather than depend on having both source and traffic.

## Decision

**Route inventory (`routes` table).** `routes(project_id, method, path, handler_file, handler_line,
framework, source, observed, …)` UNIQUE per `(project_id, method, path)`. A route comes from source
extraction (`handler_file` set) or from traffic (blank `handler_file`, `source=traffic`).

**Route extraction — `route-map` capability.** Runs semgrep with a **bundled, offline** starter ruleset
(`pkg/capability/routes.yml`, embedded and staged to a temp dir mounted read-only at `/rules`) covering
Flask/FastAPI, Express, Go net/http, and gin. Each rule captures the path in a `$ROUTE` metavariable and
declares `method`/`framework` in metadata; `pkg/interpret/routes.go` reads semgrep JSON into routes
(handler file:line from the match). The ruleset is a deliberately small, extensible starting point —
detection is heuristic (declared entry points, not a call graph). The engine branches on the route media
type to upsert routes instead of creating observations.

**Traffic confirmation (`ReconcileObservedRoutes`).** A declared route whose path template-matches an
observed request path (segment-wise; `{id}` / `:id` / `<int:id>` / `*` match any segment; exchange path via
`url.Parse`) is marked `observed=1` — confirmed exposed. A live request with **no** declared route is
recorded as a **traffic-only** route. This is the graceful-degradation path: with only proxy captures and no
source, the exposed-endpoint inventory still populates.

**Finding association (`correlateExposedRoute`).** In the engine's post-interpret enrichment, a finding
whose location file declares a route — and the route is exposed (traffic-confirmed, or the service is
exposed at all) — gets `exposed_route = "METHOD /path"` and `route_observed`. This is **file-level
proximity** (the finding sits in the handler file), not call-graph reachability from the route. A 🌐 route
pill surfaces it in triage.

## Consequences

- **"Exposed" gets specific.** Instead of "the service is exposed", a finding can say "in the handler of
  `POST /query`, traffic-confirmed" — a concrete entry point for triage and the investigation seed.
- **Degrades with partial info** (James's point). Source only → declared inventory, exposed-inferred; traffic
  only → observed-endpoint inventory, no per-finding link; both → confirmed + associated. Missing either
  never breaks routing — `exposed_route` is additive, absent when unknown, and the stack falls back to the
  project-level `exposed` signal (and then to nothing).
- **Honest about the link.** File-level proximity, clearly labeled; a finding in a multi-purpose file may
  associate to a route it isn't actually reached from. Real call-graph route→sink reachability is future
  work. Routing **gates** are unchanged this pass — `exposed_route` is context/pills, not a new gate.
- **Starter ruleset.** Framework coverage is intentionally small and extensible; route detection is
  best-effort. Method is captured where the framework encodes it (Express/gin/FastAPI verbs); Flask defaults
  to any.

## Live-test fix (2026-07-19)
End-to-end testing (daemon + real opengrep) showed a finding in a file with several routes was tied to an
arbitrary route (a SQLi in the `/search` handler mislabeled `/login`). Fixed: `nearestRoute` picks the route
whose registration is closest at or above the finding's line — the handler it actually sits in. (Also fixed
in the same session: SARIF severity for opengrep/semgrep registry rules is on `rule.defaultConfiguration.
level`, which the interpreter didn't read — every SAST finding came out `info` and never escalated.)

## Out of scope — later
Call-graph route→finding reachability; a dedicated routes graph/surface (pill only now); making
`exposed_route` a disposition gate; broader framework coverage; shipping the ruleset to **remote** runners
(same constraint as source-scan `/src` today); Flask `methods=[…]` method inference.

Composes with ADR-0030 (`exposed` enrichment + pills), ADR-0032 (SAST findings whose `dataflow_source` this
complements), and ADR-0028/0029 (the enrichment/attribute pipeline).
