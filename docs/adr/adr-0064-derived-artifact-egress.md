# ADR-0064 — Derived-artifact egress carve-out

Status: Accepted. Scanner-derived artifacts (findings, observations, investigations, coverage, dependency
inventory) are classified at a configurable "derived" tier for LLM egress instead of always private-by-
default, so an engagement can let scan **output** reach an external model while the raw source it was
derived from never leaves the machine. The tier defaults to the top level (behavior unchanged) and is
lowered deliberately per engagement.

## Context

The data-egress gate (ADR-0011/0020, `service.executeFor`) is default-deny: sending any project content to
an external model is blocked unless the destination's data clearance covers the content's sensitivity.
Tools fall into three buckets: `egressSafeTools` (return no content — always allowed), `assetEgressTools`
(send an asset's own content — gated at the asset's sensitivity), and **everything else, which is
private-by-default** — it must reach a destination cleared for the top tier.

That "everything else" bucket lumps together two very different kinds of content: raw material (ingested
documents, captured HTTP traffic, corpus/KB search, artifact bytes) and **scanner-derived analysis**
(findings, observations). The result blocks a workflow this platform is meant to enable, surfaced in real
use: *an assessor working on source they are not permitted to send to an LLM, who runs the scanners
locally (in Docker) and wants to reason over the **findings** with the model.* The scan already ran on the
sensitive source locally; only the derived output would egress. Yet `list_findings` demanded top-tier
clearance, so with a default `open_source`-cleared provider the Analyst couldn't read the very findings it
had just created (`create_finding` is egress-safe — it returns an id — but reading them back sends their
content, so it was gated).

Raising the provider's clearance is the blunt fix, but it also unlocks raw source/traffic. We want a
narrower control: send the *derived* output, keep the *source* local.

## Decision

**1. A fourth tool bucket, `derivedEgressTools`** (`pkg/analyst`): `list_findings`, `get_finding`,
`list_observations`, `list_investigations`, `get_coverage`, `list_dependencies`. These return
scanner-derived analysis, not raw content. Deliberately **excluded** — and left private-by-default —
are context/documents, corpus/KB search, captured HTTP exchanges, and artifact bytes, any of which can
return raw material.

**2. A configurable derived tier.** A global setting (`egress_derived_tier`, a classification level id)
sets the sensitivity that derived-artifact tools are treated as in the gate. `service.executeFor` gains a
branch: for a `derivedEgressTools` call, `required = derivedTier` instead of `sc.Max()`. It **defaults to
the top tier**, so nothing changes until an operator lowers it. Asset tools are unaffected — raw source
stays gated at each asset's own sensitivity.

**3. Reachable from the terminal.** The setting is exposed over the API
(`GET|PUT /v1/analyst/derived-egress`) and the CLI (`osb dlp derived [level]`), not only the desktop
Settings — because the motivating user is at a terminal over SSH (ADR-0063) and must be able to set policy
without the GUI. The blocked-tool error message now names the derived remedy ("lower the derived-artifacts
egress tier") distinctly from the asset remedy.

## Consequences

**Easier.** The intended "sensitive source, shareable scan output" assessment becomes a per-engagement
policy toggle rather than an all-or-nothing clearance bump: lower the derived tier to what the provider is
cleared for, and the Analyst reasons over findings/observations while `read_file`/`grep_code`/
`run_capability` on private assets stay blocked. Safe by default — existing deployments see no change until
the tier is lowered.

**Accepted trade-off (the residual leak).** A derived artifact can still quote sensitive source — a tainted
snippet in a finding's description, a redacted secret in an observation, a file path. Lowering the tier
trusts the scanners' abstraction and accepts that residual egress. This is a deliberate operator choice,
which is why it is off by default and set explicitly. Follow-on hardening worth considering: scrubbing/
redacting embedded excerpts before egress; a per-finding sensitivity that can be reviewed down from the
source's; and a DLP event when a derived artifact egresses under a lowered tier (the DLP log already exists,
ADR-0062).

**Scope.** Global setting for now, not per-project/per-target; a restricted engagement (ADR-0051) still
clamps the destination clearance tighter regardless, so the carve-out cannot widen a restricted engagement.
The bucket membership is a code-level allowlist — adding a new derived read tool means adding it to
`derivedEgressTools`, or it stays private-by-default (fail-safe).
