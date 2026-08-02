# ADR-0063 — Terminal client: a Claude-Code-style agent REPL

Status: Accepted — implemented (v1). We add `osb tui`, a full-screen-optional terminal client built on Bubble Tea, as a
**peer client** of the desktop GUI over the same control-plane API (ADR-0001) — the interface *is* the
Analyst conversation, not a reduced CLI or a multi-pane dashboard. The clients own no state, so a session
started in the GUI and one in the terminal are the same session; getting there requires a bearer-authenticated
SSE consumer in `pkg/client`, broader event-bus coverage, and a small amount of terminal UX. This first ADR
covers the client architecture and the backend event-coverage it forces; panels and headless bootstrapping
are named as deferred.

## Context

OpenSecBench already separates the control plane from its clients (ADR-0001): `cmd/daemon` runs the plane
headless, `cmd/osb` is a thin CLI over `pkg/client`, and the Wails desktop embeds the same plane in-process.
Assessment engineers who live in a terminal — and anyone working over SSH or on a remote box — have no
first-class way in. The CLI is scriptable but not a *place you work*; the GUI needs a desktop.

The obvious framing (a "TUI" with a dozen dockable views — Findings, Evidence, Tool Runs, …) is the wrong one.
Engineers are now fluent with agent REPLs (Claude Code, Aider): you *ask*, and the agent shows you the code,
builds the PoC, runs the scan. The terminal's job is to be that conversation, well — not to re-skin the GUI's
surfaces in ANSI. When a GUI *is* open alongside, the agent already drives it (ADR-0053's `show` tool over the
`ui.command` event), so the rich visual surface is the GUI, steered by the same agent you're talking to in the
terminal. The terminal stays light because it doesn't have to render a findings table — it asks for one.

Crucially, most of what this needs already exists, and a survey of the code (not assumptions) shows exactly
where the edges are:

- **The streaming path is already HTTP, and shared.** ADR-0053 made live turns stream over the per-project SSE
  bus (`analyst.delta` + `analyst.message`), and the GUI consumes it via `fetch` + `ReadableStream` (not
  `EventSource`) precisely so the ADR-0061 bearer token can ride as a header. There is **no** in-process-only
  streaming path to migrate — a Go client can consume the identical stream.
- **Interrupt exists:** `POST /v1/activity/agents/{id}/cancel` cancels an in-flight run via the analyst
  `RunRegistry`.
- **Join-in-progress falls out for free:** because deltas/messages are published to every subscriber, a client
  that attaches mid-turn receives everything produced after it connects; prior history comes from a thread GET.
- **Co-driving already rides the bus:** the agent's `show` publishes `ui.command`, applied by any client that
  opted into Drive — so a terminal-initiated turn drives an attached GUI with no new code.

And the real gaps, also confirmed against the code:

- **`pkg/client` sends no bearer token and has no SSE consumer.** It predates ADR-0061; against an authed
  daemon the CLI itself is currently unauthenticated-only. Both are foundations the TUI and the CLI share.
- **Domain events aren't on the bus.** Published today: `analyst.delta/message`, `exchange`, `proxy`,
  `intercept.*`, `methodology.item`, `action.run`, `ui.command`. **Not** published: task/scan completion,
  new findings, new observations, or approvals. So "watch scans and findings land as they complete" — a core
  reason to sit in the terminal — has nothing to render yet.
- **Approvals aren't broadcast.** They surface via `GET /v1/approvals` + a notification (`notifyIfPending`),
  not as a bus event.

## Decision

**1. `osb tui` — one binary, a new `pkg/tui`.** The TUI lives in `pkg/tui` (Bubble Tea + Lip Gloss + Bubbles +
Glamour) and is launched by the existing `cmd/osb`: `osb tui`, and bare `osb` when stdin/stdout is a TTY and no
subcommand is given. All existing `osb <subcommand>` behavior is untouched. No business logic enters the TUI —
it is a presentation layer over `pkg/client`, exactly as the GUI is over the API.

**2. The client is the boundary; extend it, don't bypass it.** Two additions to `pkg/client`, both shared with
the CLI:
   - **Bearer auth.** The client reads the ADR-0061 token from `controlplane.APITokenPath(dataDir)` (honoring
     `OSB_API`/explicit `--addr`) and sends `Authorization: Bearer …` on every request. This also fixes the
     CLI against an authenticated daemon.
   - **`Attach` — the one real-time primitive.** A streaming consumer of `GET /v1/projects/{id}/events` (the
     same fetch-stream wire the GUI uses), decoding `event:`/`data:` frames into a typed Go channel of events,
     with automatic reconnect. Everything the TUI does live is built on this.

**3. `attach(thread)` unifies resume, session-hopping, and reconnect.** There is no separate "resume" feature.
Attaching to a thread is: fetch its message history (`GET` thread), subscribe to the project stream, and
reconcile live `analyst.delta`/`analyst.message` frames against that history (mirroring the GUI's
"authoritative refresh at turn end" reconciliation). The *same* operation yields resume-on-relaunch (reattach
last thread), connect-to-any-existing-session (attach a chosen thread), and mid-turn SSH-reconnect (reattach
after the socket drops). A freshly attached client learns a turn is already running from the activity registry
and paints the streaming state correctly.

**4. The session is project-scoped and conversation-shaped.** Launch flow: attach/spawn a daemon → pick a
project → pick a thread to resume or start a new one → the conversation. The interface is the thread: an input
line, live-streaming assistant text, inline tool activity, and — woven into the same scroll — ambient domain
events (see 6). A thin status line shows project · findings · running tasks · a pending-approval badge.
Switching projects (`/project`) and glance-panels (transient one-at-a-time overlays for findings/tasks/
approvals) are **deferred** (see Consequences), but the conversation model leaves room for both without rework.

**5. Terminal UX, decided:**
   - **Inline (normal buffer), not the alternate screen.** Preserving native scrollback and mouse
     copy-paste beats a full-screen app frame, because the work is constantly copying payloads and `file:line`
     refs.
   - **Esc interrupts the running turn** (→ `POST /v1/activity/agents/{id}/cancel`); **Ctrl-C twice exits**
     the app (a single Ctrl-C shows a "press again to quit" hint, never a trap). Input may be queued while a
     turn streams.
   - **Glamour** renders markdown/code answers; source the agent `show`s is rendered inline as a highlighted
     snippet (the headless analogue of ADR-0050 click-to-file). With a GUI attached, the same turn also drives
     the GUI via `ui.command` — one ask, both surfaces.
   - **Client-local enhancements are allowed and expected** (autocomplete for slash-commands / thread & project
     names / cwd paths, key hints, theming). These are presentation, not state, so they never threaten parity.

**6. Broaden event-bus coverage — the backend half, which upgrades the GUI too.** Publish the missing domain
events so *any* client can react instead of poll: task/capability lifecycle (`task.*` — queued/progress/done),
`finding.*`, and `observation.*`, plus `approval.requested`/`approval.resolved`. The TUI weaves these into the
conversation scroll as muted, timestamped activity lines; the GUI routes the identical events into its findings
table and progress bars (findings that need a manual refresh today then populate live). This is "same bus,
different presentation" — the divergence is a per-client rendering choice, not state.

**7. Approvals are non-blocking.** A turn still pauses server-side for a gated tool, but the human is never
forced into a modal: the status-line badge (fed initially by `GET /v1/approvals`, instantly once `approval.*`
is on the bus) signals a waiting decision; the human views and approves/denies when ready via the existing
`POST /v1/approvals/{id}/decide`. Decisions are broadcast so a second attached client sees the badge clear.

**8. "Everything is the same" is an invariant, enforced by construction.** All content — threads, messages,
findings, tasks, and agent-behavior settings (autonomy, model) — is control-plane state consumed over one bus.
Asking either client to "run the scan suite and analyze" does identical server-side work; results stream to
every attached client at once. Only presentation differs per client. This is not a feature of the design; it
*is* the design, and the shared `pkg/client` boundary is what keeps it true rather than aspirational.

## Consequences

**Easier.** A terminal-native, SSH-friendly way to run an assessment as a conversation, with the agent showing
code and building PoCs where you already work. Event-coverage (item 6) makes the GUI go live for findings and
task progress as a side effect — a real-time win both clients share. The bearer-auth fix (item 2) makes the CLI
correct against an authenticated daemon. A future web client inherits the same `Attach` contract for free.

**Harder / accepted trade-offs.**
- **Reconnect discipline.** The event hub deliberately drops events for a stalled subscriber (availability over
  delivery), so streamed deltas are ephemeral. The TUI must treat deltas as throwaway and rely on a thread
  refetch on reconnect/turn-end for the authoritative record — the same contract the GUI already honors. Get
  this wrong and an SSH blip looks like lost output.
- **Ambient-event noise.** Weaving domain events into the chat scroll risks drowning the conversation; they are
  rendered muted/secondary, and a verbosity control is likely follow-on.
- **Two presentations of one event stream** means two renderers to keep coherent as event types evolve; the
  typed event channel in `pkg/client` is the shared seam that limits the blast radius.
- **Terminal display is an unredacted channel.** Findings, tokens, canaries, and PoC payloads stream into a
  terminal that may be tmux-logged or session-recorded; ADR-0062 DLP governs *LLM egress*, not *screen output*.
  A display-redaction posture is noted as follow-on, not solved here.

**Deferred (named, not designed here).**
- **Glance-panels** — transient, one-at-a-time overlays (findings/tasks/approvals) summoned by key/slash and
  dismissed with Esc. The conversation-first model is chosen so these can be added without restructuring.
- **Headless bootstrapping.** v1 assumes the project *and* the LLM-provider connection were created in the GUI
  (or via `OSB_LLM_*`); the TUI attaches to what exists. A terminal-only "create project / add a provider"
  flow — needed for a truly GUI-independent SSH experience — is future work.
- **`/project` switching**, **anchored co-drive deep-refs** (driving the GUI to a *specific* finding, not just
  a surface), and **`osb session attach`** raw-terminal streaming (the existing P7 TODO) round out the backlog.

**Before changing this,** know that the parity invariant (item 8) is load-bearing: any client-side state that
isn't pure presentation, or any streaming path that isn't the shared bus, quietly breaks "everything is the
same." Keep new real-time features flowing through `pkg/client.Attach` and the event bus.

## Implementation notes (v1 delivered)

All eight Decision items shipped across five phases (`03f15d8` client bearer + `Attach`; `7f861d0` TUI shell;
`c8a7e94` turn UX; `8e00eee` event coverage; `bb5115c` terminal approvals), plus post-phase extras
(`6f5308e` `/findings`+`/observations`, `96b0ebe` `/search`, `9b86750` dir-local project create). Deltas from
the design as written above, recorded for honesty rather than left implicit:

- **Item 6 event coverage shipped narrower than the enumerated set, by design.** `task.completed` (with an
  `observation_count`) and `finding.created` are published; `task.*` queued/progress and per-`observation.*`
  events were **intentionally dropped** to avoid drowning the conversation scroll — observations surface via
  `task.completed`'s count plus the pull-based `/observations` command (`8e00eee` commit rationale). Only
  engine/disposition-origin findings emit `finding.created`; analyst/API-created findings still ride
  `analyst.message`.
- **GUI-side rendering of the new bus events is follow-on.** The events are on the bus (upgrading the GUI is
  the stated side benefit of item 6), but the React handlers that route them into the findings table / progress
  bars aren't wired yet. Tracked in `docs/TODO.md`.
- **`/project` switching shipped** (`tui.go`), despite being listed as deferred above.
- **Still deferred as written:** glance-panels, headless *provider*-connection creation (dir-local *project*
  creation shipped; LLM providers remain GUI/`OSB_LLM_*`-first), anchored co-drive deep-refs, and
  `osb session attach` raw-terminal streaming.
