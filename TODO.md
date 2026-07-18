# TODO backlog

Future work and deferred decisions. **Rule: never stub something out without adding a TODO here**
(and a matching `// TODO:` in code). Items graduate into [TASKS.md](TASKS.md) when we pick them up.

## Decisions to confirm

- [ ] **OSS license** — Apache-2.0 (patent grant, common for security tooling) vs MIT. No LICENSE
      committed yet; decide before the repo goes public.
- [ ] Name for the chat/thread container concept (the Analyst works in threads — call them
      "investigations", "cases", or keep "threads"?).

## Setup / environment

- [x] Install Wails CLI; create the GitHub org/repo and wire the remote.
- [ ] **Verify the Wails desktop build locally.** The build environment lacks
      `libwebkit2gtk`, so `main.go` (the `desktop`-tagged Wails entrypoint) could not be compiled
      or run there. On a machine with `libgtk-3-dev` + `libwebkit2gtk-4.1-dev`, run `wails dev`
      and fix any wails API mismatches. The React frontend itself is verified (browser).

## Cross-cutting gaps

- [ ] **Wire the append-only audit log into the control plane.** `pkg/audit` exists and the Analyst
      takes an audit callback, but scope blocks, Repeater sends, and capability runs are not yet
      appended to a persisted audit trail — their durability currently rests on the task/exchange
      rows. Add an `audit.Log` to the control plane + API and record these governed actions
      (ADR-0002, ADR-0007). Until then, "audited" in ADR-0007 means the durable DB row.

## Deferred subsystems (tracked in the roadmap, not yet built)

- [ ] Remote outbound-connect runner + split `opensecbench-runner` repo (P12).
- [ ] Community extension hub with publisher verification / signing / scanning (P12).
- [ ] Hosted team service (only if existing-platform sharing proves insufficient).
- [ ] RDP/VNC remote-desktop sessions (P12); terminal (SSH/PTY) first in P7.
- [ ] RAG index for large corpora (P12); tool-based read/grep retrieval first.
- [ ] DOCX report export (P12); MD/HTML + PDF first.
