# ADR-0065 — Data-centric egress: gate content, not tools

Status: Proposed. The data-egress boundary stops classifying *tools* and starts scoping the *data* they
can reach: a tool's accessible domain is confined at the data-access layer to items the destination is
cleared for, so sensitive assets are never read into the agent or the model — prevention, not
post-execution filtering. Whether a scanner-derived artifact (finding/observation) is treated as its own
lower tier or inherits the source's sensitivity becomes a per-project governance choice on the Engagement
record, defaulting to derived = lower. Supersedes the tool-identity gate (amends ADR-0011/0020/0062,
generalizes ADR-0064).

## Context

Egress today is gated per *tool* (`analyst.executeFor`, ADR-0062): a tool is `egressSafeTools`,
`assetEgressTools` (asset sensitivity), `derivedEgressTools` (ADR-0064 tier), or private-by-default —
and the whole tool is blocked pre-execution if the destination's clearance doesn't cover its class. This
is coarse, and it breaks real use: with an `open_source`-cleared connection the Analyst can't even
*orient* — `list_projects`/`list_findings` return `blocked by data-egress policy` because the tool
*might* return private content, regardless of whether the actual result contains any. A fresh project
opens to a wall of egress errors.

The problem is that the unit of classification is wrong. A tool is not sensitive; the *data it returns*
is. `list_findings` over a project with only open-source findings leaks nothing; the same tool over a
private repo's raw findings might. Blocking the tool can't tell the difference, so it blocks always.

The fix (James's framing): **gate what the tools operate on, not the tools** — and enforce it as
*prevention*. The agent must never be given access to sensitive assets in the first place; filtering a
result *after* the tool loaded that data is the wrong posture (default-allow-then-subtract, one filter
bug from a leak, and the sensitive data has already transited the agent). Confine the tool's reachable
data instead.

## Decision

**1. Scope the reachable data; never load what's over clearance.** The guard is enforced at the
data-access layer, not after execution:
- **List/query tools** run, but their store query is **scoped** to items at or below the destination's
  clearance — the over-clearance rows are *never selected* (a `WHERE` predicate, not a fetch-then-drop).
  The agent sees the cleared set, plus at most a count of what's withheld (metadata only, no content).
- **Specific-item reads** (`read_file`, `get_finding`, `read_context`, `get_exchange`, `read_artifact`)
  are **refused before the item is read** when the target's sensitivity exceeds clearance — the bytes are
  never loaded. The refusal names why, so the model can guide the human.
- **Local destinations** are unconstrained (nothing leaves the machine).

So a sensitive asset is simply outside the agent's accessible domain; nothing sensitive transits the
agent to be filtered later.

**2. Every returnable item carries a sensitivity tier**, drawn from the shared classification scale
(ADR-0011). Provenance by category:
- **Raw source / asset content** (file bytes, `grep_code`, a capability's raw stdout) → the **asset's**
  sensitivity. Stays local for a private asset.
- **Derived artifacts** (findings, observations, coverage, dependency inventory) → per the per-project
  policy in (3). This is ADR-0064's tier, generalized from a tool flag to a property of each item.
- **Metadata** (project/application names, ids, status) → its own tier; conservative by default because a
  project name can itself be sensitive (a client name).

**3. Derived-artifact classification is a per-project governance choice** on the Engagement (ADR-0051),
alongside `DataClass`:
- `derived` (**default** — James's model): derived artifacts get a configurable derived tier, *lower*
  than the source they came from, because they're abstractions of it. This realizes "assess source you
  can't send, but share the scan output."
- `inherit`: derived artifacts take the sensitivity of the source asset/project they were derived from —
  the stricter position (a finding from private code is private). For shops that don't accept the
  abstraction argument.

The per-project derived tier + mode replace ADR-0064's global `egress_derived_tier` setting (which
becomes the default seed). Existing per-project tightening still applies and only ever tightens: a
`restricted` engagement clamps the destination and can force `inherit`.

**4. `run_capability` returns a derived summary, not raw output.** So the agent can drive local scans on
private assets and see the result: the tool returns "done · N observations" (derived), and the
observations themselves are read via `list_observations`, filtered by their derived classification. The
raw scanner stdout (asset-sensitivity) is stored locally but not dumped into the model context.

**5. Enforcement is per-tool but centralized, and fails safe.** Each read tool declares either the scope
predicate for its query (list tools) or the sensitivity of the item it targets (item reads), consumed by
one egress-aware executor. A read tool with no declared rule refuses external access by default — the
same default-deny posture, now at the data-access layer. Write tools (return only an id/status) and
static catalogs are unaffected.

**6. Scope.** This ADR governs the **governed-tool / prompt** path — content the executor places in the
model's context. Agents that reach data through a **shell in a sandbox** (run_code and future sandboxed
agents) are governed separately by **ADR-0066** via mount composition + model locality, because a shell
bypasses the resolver entirely. Both enforce the same invariant; a capability that can egress must pass
through one of the two.

## Consequences

**Easier.** The Analyst is usable out of the box on a least-cleared connection: it always orients and
operates, and only sensitive *data* is withheld — with the model told what and why, so it can guide the
human ("raise clearance / lower the derived tier to see the 3 withheld findings"). James's workflow
(local scans, shareable derived output, source never leaves) becomes the default behavior, not a set of
tool-flag exceptions. The stricter `inherit` shops get a one-setting switch.

**Harder / accepted.** Per-tool scope rules are more code than a per-tool boolean, and must stay correct
as tools and result shapes evolve — the fail-safe (a rule-less read tool refuses external access) bounds
the risk. Enforcement lives at the store/access layer, so scoped reads need sensitivity-aware queries
(findings/observations must resolve a sensitivity to filter on — the per-project derived policy supplies
it). This does NOT enable sending *transformed* private content (redacting/obfuscating a private file so
it can egress); that is a separate, deliberate capability (the sanitize-for-sharing work in docs/TODO.md),
and is out of scope here — this ADR only ever withholds, never transforms-to-share.

**Migration / phasing.** Large enough to stage: (1) per-project derived policy on the Engagement + the
sensitivity resolver (seeded from the ADR-0064 global default); (2) sensitivity-scoped list/query reads
for the orientation tools (fixes the wall-of-errors immediately) — a scoped read replaces a blocked tool;
(3) per-item access refusal for the specific-item reads + the `run_capability` derived summary, retiring
the coarse tool-identity gate; (4) a DLP event per withheld access, turning the boundary observable. The
tool-identity sets (`assetEgressTools` etc.) remain during migration and are removed in (3).

**Before changing this,** the invariant is: nothing above the destination's clearance is ever read into
the agent or the model. Enforcement is at the data-access layer (scoped queries, refused item reads), not
after the fact — the sensitive data is never loaded, not loaded-then-stripped.
