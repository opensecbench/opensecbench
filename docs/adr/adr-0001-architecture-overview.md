# ADR-0001 — Architecture overview & boundaries

Status: Accepted

## Context

OpenSecBench is a local-first security assessment workbench that must also support, over time,
headless/CLI use, automation, and (eventually) team collaboration. It runs dangerous security
tooling and handles sensitive client data, so isolation, provenance, and auditability are
load-bearing — not features to retrofit. The primary client is a desktop app (Wails + React),
but the desktop framework must never become the home of domain logic.

## Decision

**A standalone, headless Go control plane is the core.** It owns all domain logic and state and
exposes a **local HTTP API** on loopback. Every client — the Wails desktop app, the `osb` CLI,
and any future web/team client — is a thin client against that API.

- The Wails desktop app **boots the control plane in-process** (a goroutine on `127.0.0.1`) and
  the React frontend talks to it over the HTTP API, exactly as the CLI does. Wails bindings are
  used **only** for OS-native concerns (file dialogs, revealing paths). Domain packages under
  `pkg/` must never import Wails; CI enforces this.
- `cmd/daemon` runs the same control plane headless (no UI) for CLI/automation/future team use.

**Layered subsystems**, each a `pkg/` package with a narrow interface:

```
clients (cmd/desktop · cmd/osb) ──HTTP──► pkg/api ──► control-plane services
  store · cas · search · capability · extension · template · playbook · task ·
  runner · scope · proxy · session · agent · policy · llm · secret · dlp ·
  integration · collab · kb · viz · report · audit
```

Foundational invariants that every subsystem upholds:

1. **Provenance-first.** Results carry lineage back to the task, capability+version, tool+version,
   and runner that produced them. AI output never silently becomes evidence (ADR-0002).
2. **Human in control.** The agent (the *Analyst*) acts only through the capability system —
   never an ungoverned host shell — and its actions pass an approval gate and are audited.
3. **Sandboxed execution.** Capabilities run in isolated runners with resource/network/filesystem
   limits (ADR-0004).
4. **Everything extensible.** Capabilities, methodologies, templates, playbooks, visualizations,
   and report templates are open-format packages; built-ins use the same public contracts
   (ADR-0003).
5. **Append-only audit.** Every action is recorded immutably (ADR-0006).

## Consequences

- We build and maintain our own HTTP API surface and (later) our own agent runtime, rather than
  leaning on the desktop framework or a vendor agent CLI. This is deliberate: it keeps every
  future client and deployment mode cheap and keeps control/audit in our hands.
- The in-process-daemon pattern means the desktop app and a headless daemon share one codebase;
  the only difference is who starts the loopback listener and whether a UI is attached.
- A boundary test (`pkg/` must not import Wails) is part of CI from the start.
- Loopback-only binding for the API in single-user mode; authentication/transport hardening is a
  concern for the future team service, not the local daemon.
