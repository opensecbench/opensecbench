# ADR-0067 — `web_service`: a first-class HTTP target primitive

Status: Accepted. Add a `web_service` asset type — a reachable HTTP(S) target at a base URL, in one
environment — so a live site (browser app *or* API) becomes a first-class, scannable, scope-checked,
traffic-attributable entity instead of being misfiled as a `source_repo` or lost as an untracked runtime
`target` param. Per-asset environment and an automation ceiling let the same logical application be tested
at different intensities across dev/qa/prod, and captured proxy traffic is stamped with the owning service.

## Context

OpenSecBench models what you assess as **assets** under an **application** (ADR-0049): today
`source_repo`, `cloud_deployment`, `infrastructure`, `document`, `correspondence`. A survey of the code
shows there is **no home for "the live website you're testing"**:

- **No asset type carries a URL.** `Asset.Location` is a generic string the source scanners treat as a
  directory; the engine only reads `source_repo` locations and explicitly rejects other kinds as scan
  targets (`pkg/task/engine.go:352-360`). `cloud_deployment`/`infrastructure` exist but the only thing that
  reads them is the exposure signal (`pkg/store/exposure.go:68`) — nothing *scans* them.
- **The create form misfiles URLs.** `EngagementModal`'s "repo or base URL" field hardcodes the asset as
  `source_repo` (`frontend/src/EngagementModal.tsx`), so `https://shop.acme.com` becomes source code a
  scanner tries to mount as a folder.
- **Network tools opt out of auto-scan.** Capabilities declare `AppliesTo []string` (the asset kinds the
  scan orchestrator fires them against — `pkg/capability/capability.go:34-37`). Every static tool binds to
  `source_repo`; the network tools (`http-probe`, `nmap`) declare **no** `AppliesTo` and instead take a
  runtime `target` param (`builtins.go:230,263`) that is scope-checked (`engine.go:1436-1467`) but **never
  persisted** as "the site." So there is nothing for "Scan everything" to point them at.
- **Rules of engagement are engagement-wide only.** A capability's `Technique` (`intrusive`, `brute_force`,
  `dos`, …) is gated against the *engagement* (ADR-0051), not per asset. There is no way to say "passive
  only against prod, intrusive OK against dev."
- **Proxy traffic isn't attributed.** Captured exchanges record the host but are not tied to the asset/
  environment they belong to.

The result: the single most common assessment subject — a web app across dev/qa/prod — has no fitting
primitive, and the machinery that *would* serve it (`AppliesTo`, the `target` param, scope, the technique
gate, exposure) is all present but unconnected.

## Decision

**1. New asset type `web_service`.** One primitive for a reachable HTTP(S) target at a base URL, covering
both browser apps and APIs — from the tooling/scope/proxy view they are handled identically, so this is
one type with a descriptor, not three (`web_server` is the wrong level — that is a host/`infrastructure`
concern; `web_app` collides with the existing **Application** grouping entity). `Location` holds the base
URL (`http://localhost:8080`, `https://shop.acme.com`). The same logical application is deployed as
sibling `web_service` assets under one `Application`:

```
Application "Storefront"
 ├─ web_service  dev   http://localhost:8080   automation: aggressive
 ├─ web_service  qa    https://qa.acme.com     automation: active
 └─ web_service  prod  https://shop.acme.com   automation: passive
```

**2. Per-asset `environment` and `automation` ceiling.** `web_service` assets gain two fields:
- `environment` — `dev` | `qa` | `staging` | `prod` (descriptive; also surfaced on attributed traffic).
- `automation` — an ordered ceiling on tool intensity for this asset:
  `off` < `passive` < `active` < `aggressive`.

**3. Scan wiring — bind network/DAST tools to `web_service` and source their target from it.** The
network capabilities gain `AppliesTo: ["web_service"]`, so the scan orchestrator (`ScanProject`) fires
them against `web_service` assets. When it does, it fills the capability's `TargetParam` from the asset's
`Location` (the base URL) instead of requiring a hand-typed target. Scope enforcement is unchanged: the
resolved target still must pass the project's host/domain/cidr allowlist (`engine.checkScope`). A
`web_service` with no matching scope entry is skipped with a clear reason (as ecosystem-mismatch is today).

**4. Automation ceiling = a second technique gate.** Today `engine` blocks a run unless the *engagement*
permits the capability's `Technique`. Adoption adds a second, per-asset gate for runs against a
`web_service`: the technique must be allowed by **both** the engagement **and** the asset's `automation`
level. The mapping:

| automation | permits techniques |
|---|---|
| `off` | none — no automated capability runs against this asset (manual/proxy only) |
| `passive` | passive only (`Technique == ""`) |
| `active` | passive + `intrusive` |
| `aggressive` | passive + `intrusive` + `automated_exploit` + `brute_force` |

`dos`, `destructive`, and `social` are **never** enabled by the automation ladder — they remain
out-of-band, gated solely by explicit engagement RoE, so a high automation level can never quietly unlock
them. The gate is escalate-safe: it can only *narrow* what the engagement already allows.

**5. Proxy attribution.** Captured exchanges (`pkg/proxy`, ADR-0016) are stamped at capture time with the
`web_service` asset whose base-URL host (`host[:port]`) matches the exchange's host, via a new nullable
`asset_id` on the exchange. Ties (multiple services sharing a host) break by longest base-URL path-prefix
match against the request URL; no match leaves it null. The Proxy/Replay surface shows and filters by
service + environment, so traffic is always identifiable — the primary requirement.

**6. Create-form fix.** `EngagementModal` chooses the asset type by value: a `://` URL → `web_service`
(with `environment`/`automation` selectors, defaulting `prod`/`passive` — the safe default), a path or git
remote → `source_repo`. This supersedes the current hardcoded-`source_repo` behavior.

**7. Exposure parity.** `web_service` joins `cloud_deployment`/`infrastructure` as a source of the
"exposed" reachability signal (`pkg/store/exposure.go:68`), so a URL-bearing service feeds ADR-0030
reachability/exposure routing for free.

This amends ADR-0051 (technique gating becomes per-asset for `web_service`), extends ADR-0028/0030
(new observation-bearing target kind; exposure source), and ADR-0016 (exchange→asset attribution). It does
**not** change how `source_repo` assets or the static toolchain behave.

## Consequences

**Easier.** The most common assessment subject finally has a fitting primitive: a live site is a scannable
asset the orchestrator can target, scope-checked, with per-environment intensity, and every captured
request is attributable to it. `AppliesTo`, the `target` param, scope, the technique gate, and the
exposure signal — all previously disconnected — now compose for web targets.

**Harder / trade-offs.**
- **Automation ladder is a mapping, not a free dial.** Collapsing techniques into four tiers is a
  deliberate simplification; a future need for finer per-tool control would extend, not replace, it.
- **Attribution is best-effort.** Host-match + path-prefix covers the common cases; multiple apps behind
  one host with overlapping paths, or a shared reverse proxy, can leave traffic unattributed (null, not
  wrong). Acceptable — better an honest null than a misattribution.
- **Scope stays separate.** Adding a `web_service` does not auto-add its host to the in-scope allowlist;
  the form should *offer* to (one click), but scope remains the deliberate authorization record, not a
  side effect of adding an asset.
- **Schema change.** New asset columns (`environment`, `automation`) and an exchange `asset_id` — additive
  migrations; existing assets read as `web_service`-inapplicable and keep working.

**Phased delivery (this ADR covers all of it; build in order):**
1. **Foundation** — the `web_service` type + base-URL `Location`, the create-form fix, `AppliesTo` wiring
   so `http-probe`/`nmap` target a `web_service` (target sourced from `Location`, scope enforced), and
   exposure parity. Makes the site a scannable asset.
2. **Per-asset governance** — the `environment` + `automation` fields and the second technique gate.
3. **Proxy attribution** — the exchange `asset_id` stamp + service/environment filter in the UI.

**Deferred (named, not designed here).** A web tech-fingerprint gate analogous to source `Ecosystems`
(don't run a WordPress scanner on a bare API); path-scoped sub-services under one host; auto-discovery of
`web_service` assets from observed proxy hosts.
