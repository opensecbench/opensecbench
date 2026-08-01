# ADR-0054 — Consequence-tier governance (capability parity)

Status: Accepted — building. Reframes the Analyst's approval model from "sensitive tools the agent must ask
about" to "consequence tiers everyone follows." The gate keys on what an action's worst consequence is —
can it be undone? does it leave the host? — **not** on whether an agent or a human performs it. This makes
capability parity (ADR-0053) the default and gives the gate a principled meaning. Supersedes the actor-based
`sensitiveTools` allow-list of ADR-0019 §5 (the trust-curve override rules are kept as the fine tuner).

## Context

ADR-0019 gated a fixed set of `sensitiveTools` (send_request, run_code, create_finding, set_coverage, …):
the agent paused for approval on those, a human never did. Two problems surfaced in use:

1. **It broke parity.** The agent had *no* way to set a finding's status — a hardcoded human-only action — so
   it punted ("you'll have to close it manually"). But a human just clicks a dropdown. Per the project's
   principle (human and agent both drive; ADR-0053), the agent should have the same capability.
2. **The list was arbitrary.** `triage_observation` (disposition an observation) ran free, but `create_finding`
   asked — with no principled difference. The gate was a property of *which tool*, and of *who* called it,
   rather than of *what the action does*.

The insight: **"risky" and "agent-performed" are orthogonal.** Risk is about an action's consequences; who
performs it is a separate axis. Gate on consequence and parity falls out for free — the rule never mentions
the actor. The scope guard already works this way (it checks `send_request` by scope for the agent *and* the
human's Replay), which is the model generalized.

## Decision

**1. Every action has a consequence class** (`pkg/analyst/policy.go`), by what running it can't take back:

| Class | Meaning | Default |
|---|---|---|
| `Reversible` | internal, undoable state — create/edit/disposition | run free |
| `External` | leaves the host: outbound traffic / fetch — can't un-send | confirm |
| `Execute` | runs code / a scanner / a sub-agent — side effects + cost | confirm |
| `Destructive` | irreversible data loss | always confirm (floor) |

Only non-Reversible actions are listed (`send_request`, `web_fetch` → External; `run_code`, `run_capability`,
`run_playbook`, `delegate` → Execute). Everything else — reads, and reversible writes (`create_finding`,
`set_coverage`, `set_finding_status`, `triage_observation`, KB drafts, `workspace_write`, `show`, …) — is
Reversible and runs freely for **agent and human alike**. Oversight of the reversible tier is undo + audit,
*after the fact*, not a pre-approval prompt.

**2. The autonomy envelope is the control surface (D).** A single human-set knob — `cautious` (default;
only Reversible runs free) or `trusted` (Reversible + External + Execute run free; Destructive still
confirms) — shifts the confirm line across tiers without touching the capability set. Stored as the
`analyst_autonomy` setting; surfaced/settable via `GET/PUT` on the approval-policy endpoint.

**3. The trust curve stays as the fine tuner.** The ADR-0019 override `Rule`s (tool[,profile] → auto|approve)
still win over the tier default — so an operator can pin one action tighter or looser than its tier, at any
envelope. Precedence: explicit rule > autonomy envelope over consequence tier.

## Consequences

- **Parity by construction.** `create_finding` / `set_coverage` / `set_finding_status` now run without a
  prompt — the agent has the human's capabilities. `send_request` / `run_code` / `delegate` still confirm at
  the default envelope, because they leave the host or run work — for whoever triggers them, not because "an
  agent did it."
- **The gate finally means something.** "Why did this ask?" has a principled answer (its consequence tier),
  not "it's on a list."
- **Enforcement floors are untouched.** The scope guard and DLP/egress policy are separate walls; relaxing a
  gate removes a prompt, never a wall. `web_fetch` keeps its preapproved-source bypass (ADR-0038).
- **API stable.** `Decide` / `NeedsApproval` / `NewPolicy` / `DefaultPolicy` / `SensitiveTools` keep their
  shapes (`SensitiveTools` now = the above-Reversible tools); `ToolConsequences()` exposes the tiers for the UI.
- **Autonomy selector (landed).** A Cautious/Trusted dropdown in the Analyst header (next to Drive), backed by
  `PUT /v1/analyst/autonomy` (which sets only the envelope, leaving the override rules untouched). Trusted
  reads as elevated (amber). The setting drives `loadPolicy` end to end.
- **Follow-ups:** an activity feed + one-click undo for the reversible tier (the promised post-hoc oversight);
  revisit whether `Destructive` needs its own concrete actions (none classified yet); optionally scope the
  envelope per-project rather than global.
