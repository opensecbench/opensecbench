# ADR-0066 — Sandboxed-agent egress: mount composition + model locality

Status: Proposed (strategic direction; not yet built). For agents that run a shell and arbitrary tools in
a sandbox — the scalable alternative to building a governed tool for everything an agent might do — egress
is governed not by per-tool guards but by two levers: **which model backs the sandbox** (local vs
external) and **what data is composed into the sandbox** (only cleared assets are mounted). A local-model
sandbox is unconstrained; an external-model sandbox is built from cleared data only. This is the eventual
*primary* enforcement for capable agents; the governed-tool data-access guard (ADR-0065) remains the
lightweight path. Both hold the same invariant: nothing above the destination's clearance reaches an
external model.

## Context

The governed tool-use model (ADR-0017) requires a first-class tool for every capability — `read_file`,
`grep_code`, `run_capability`, … Real assessment work is open-ended (write a throwaway script, chain a few
CLIs, poke at a target), so tool-for-everything is a treadmill. The scalable model is a **sandboxed agent
with a shell and the files** — which we already have the primitive for: `run_code` (ADR-0020) runs
`sh -c` in a Docker sandbox with a mount, network, and output back to the agent, gated today by approval
and a private-by-default classification of its output.

But a sandboxed shell agent collides with egress in a way no gate can finesse. The point of the agent is
that it reads things and reasons about them *through its model*. So for an **external** model you cannot
have all three of: (1) arbitrary shell over the private files, (2) an external LLM, (3) no egress of that
source. Whatever the agent can read, the external model sees. Per-tool guards can't fix this — they're
trying to police an external brain that by design wants to see everything in its sandbox.

The resolution is not more tooling. It is choosing the brain and composing the sandbox.

## Decision

**1. Model locality is the primary lever.**
- **Private/sensitive source → back the sandbox with a LOCAL model** (e.g. Ollama on/near the host).
  Then mount the whole repo and grant a full shell and any tools — nothing leaves the machine, so no
  scoping is required. This is the way to run a *capable, roaming* agent on sensitive code.
- **External model (Claude/API) → the sandbox is composed from cleared data only** (below).
- **External model + operator explicitly clears the source → mount it** (a deliberate clearance choice).

**2. Sandbox composition is the data gate for external-model sandboxes.** Only assets at or below the
destination's clearance are mounted; the private ones are simply not in the container's filesystem
namespace. Enforcement is the kernel not placing the file in the sandbox — *prevention by construction*
(ADR-0065's posture, realized physically), not a policy check the agent's code could bypass. Mechanisms,
by granularity:
- **Asset-level → selective bind mounts** (default). Compute the cleared asset set; add one read-only
  bind mount per cleared asset (the runner already takes `Mounts []Mount`). OS-enforced, minimal escape
  surface. Sufficient while clearance is per-asset (assets carry a sensitivity today).
- **File-level within a repo → a materialized staging tree** of only the cleared files, bind-mounted
  read-only. Use hardlinks (cheap; a hardlink *is* the file) or copies (safe across filesystems). Not
  symlinks — a symlink to a host path outside the mount fails to resolve (or, worse, resolves to
  something that *is* in the container), and symlink/`..` races are a container-escape class.
- **Dynamic / per-read policy → FUSE** (a classification-aware filesystem, potentially redacting on read;
  ties into the sanitize-for-sharing work, docs/TODO.md). Most powerful, most code and escape surface —
  reserved for when live per-read policy is actually needed.

**3. Baseline sandbox posture:** read-only mounts, no mounts beyond the cleared set, dropped capabilities,
and network off or scoped (a local model needs no outbound; an external model reaches only its API host,
ideally via a runner egress endpoint, ADR-0025). This makes "the agent can't reach it" a property of the
kernel, not of the agent behaving.

**4. The two-tier pattern this enables** (and the point of it): a **local-model** agent roams the private
source with a full shell and emits **derived findings**; an **external-model** agent then reviews those
derived findings, if the per-project derived tier clears them (ADR-0064/0065). Capable agent on sensitive
code, source never leaves, external brain only ever sees the abstractions.

## Consequences

**Easier.** The tool layer becomes a thin convenience rather than the whole surface — "here is a sandbox
with the data you're cleared for and a brain that matches" scales to arbitrary work. It realizes the
sensitive-source workflow directly and physically, with the strongest possible enforcement (kernel
namespace isolation). It supersedes `run_code`'s tool-output classification: a sandboxed run is governed
by its mount composition + backing model, not by a per-tool egress tier.

**Harder / accepted.** Requires a local-model story to be genuinely usable for the capable-agent-on-
private-source case — an external model can *never* roam private source without egress, and no amount of
engineering changes that. Building scoped mounts (and especially a staging tree or FUSE) is real work, and
per-asset classification must be trustworthy (the mount set is only as correct as the sensitivities).
Sandbox hardening (caps, network, escape surface) is ongoing security work, not a one-time setting.

**Relationship to ADR-0065.** 0065 governs the **governed-tool / prompt** path (content the executor puts
in the model's prompt) via data-access scoping — buildable now, and the near-term fix for the wall-of-
errors. This ADR governs the **sandbox** path (data the agent reaches through a shell) via mount
composition + model locality — the eventual primary for capable agents. They are two enforcement points
for one invariant; a capability that can egress must pass through one of them, and the fail-safe default
on both is deny (unclassified content withheld; unmounted files absent).

**Before building this,** the load-bearing rule: an external model in a sandbox sees everything mounted,
period. Enforce by *not mounting* what must not leave, or by backing the sandbox with a local model —
never by trusting the agent not to look.
