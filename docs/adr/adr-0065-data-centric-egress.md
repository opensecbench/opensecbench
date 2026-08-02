# ADR-0065 — Data-centric egress: gate content, not tools

Status: Proposed. The data-egress boundary stops classifying *tools* and starts classifying the *content*
they return: tools always execute (locally, on full-fidelity data), and a post-execution filter removes
or redacts anything the destination isn't cleared for before it enters the model's context. Whether a
scanner-derived artifact (finding/observation) is treated as its own lower tier or inherits the source's
sensitivity becomes a per-project governance choice on the Engagement record, defaulting to derived =
lower. Supersedes the tool-identity gate (amends ADR-0011/0020/0062, generalizes ADR-0064).

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

The fix (James's framing): **gate what the tools operate on, not the tools.** Run the tool, then let only
the cleared subset of its result reach the model.

## Decision

**1. Tools run; results are filtered.** The egress guard moves from before `exec()` to after it. Every
tool executes against full-fidelity local data. Its result passes through an **egress filter** keyed to
the destination's clearance: items/fields above clearance are dropped or redacted, with a breadcrumb the
model can see ("3 findings withheld: private"). A tool whose entire result is above clearance returns
only that breadcrumb — never an error. Local (non-external) destinations skip the filter entirely.

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

**5. The filter is per-tool but centralized.** Each tool declares how to classify + redact its result
(a small function per result shape), invoked by one egress wrapper. Unknown/unclassifiable content
fails safe to the top tier (withheld). Adding a tool without a classifier means its content is withheld
from external destinations by default — the same fail-safe default-deny posture, now at the data layer.

## Consequences

**Easier.** The Analyst is usable out of the box on a least-cleared connection: it always orients and
operates, and only sensitive *data* is withheld — with the model told what and why, so it can guide the
human ("raise clearance / lower the derived tier to see the 3 withheld findings"). James's workflow
(local scans, shareable derived output, source never leaves) becomes the default behavior, not a set of
tool-flag exceptions. The stricter `inherit` shops get a one-setting switch.

**Harder / accepted.** Per-tool result classifiers are more code than a per-tool boolean, and must be
kept correct as tool outputs evolve — the fail-safe (withhold the unclassified) bounds the risk. Redaction
(vs whole-item withhold) is deferred: v1 withholds items/fields above clearance wholesale; in-place
redaction of sensitive spans ties into the sanitize-for-sharing work (docs/TODO.md) and lands later.
Filtering after execution means a tool does the local work even when nothing will egress — correct (the
work is local and cheap) but worth noting.

**Migration / phasing.** Large enough to stage: (1) per-project derived policy on the Engagement + the
classification resolver (seeded from the ADR-0064 global default); (2) the result-filter framework + the
orientation/list tools (fixes the wall-of-errors immediately); (3) migrate content tools (read_file,
run_capability summary) and retire the pre-execution tool gate; (4) a DLP event per withheld/redacted
egress (docs/TODO.md), turning the boundary observable. The tool-identity sets (`assetEgressTools` etc.)
remain during migration and are removed in (3).

**Before changing this,** the invariant is: nothing above the destination's clearance enters the model's
context. Whether enforced by blocking a tool or filtering its result, that property must hold — the shift
here is *where* it's enforced, not *whether*.
