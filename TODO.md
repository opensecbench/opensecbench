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

- [x] **Wire the append-only audit log into the control plane.** Done: a persisted, hash-chained
      `audit_events` table (migration 0011) records governed actions — task runs + scope blocks,
      scope changes, Repeater sends/blocks, session open/close, evidence promotions, playbook runs,
      approval decisions, and Analyst tool calls. Exposed at `GET /v1/audit` (+ `osb audit` + an
      Audit tab). Verified via CLI end-to-end; chain resumes across restarts.
- [ ] **Broaden audit coverage** to remaining mutations (project/application/asset/finding CRUD)
      and add chain-verification (`osb audit --verify`) that recomputes hashes to detect tampering.
- [ ] **Audit-write failures are best-effort** (logged, not fatal). Decide whether a governed action
      should hard-fail if its audit append fails (tamper-evidence vs availability) before P10 DLP.

## Deferred subsystems (tracked in the roadmap, not yet built)

- [ ] Remote outbound-connect runner + split `opensecbench-runner` repo (P12).
- [ ] Community extension hub with publisher verification / signing / scanning (P12).
- [ ] Hosted team service (only if existing-platform sharing proves insufficient).
- [ ] RDP/VNC remote-desktop sessions (P12); terminal (SSH/PTY) first in P7.
- [ ] RAG index for large corpora (P12); tool-based read/grep retrieval first.
- [ ] DOCX report export (P12); MD/HTML + PDF first.
