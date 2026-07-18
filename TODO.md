# TODO backlog

Future work and deferred decisions. **Rule: never stub something out without adding a TODO here**
(and a matching `// TODO:` in code). Items graduate into [TASKS.md](TASKS.md) when we pick them up.

## Decisions to confirm

- [ ] **OSS license** — Apache-2.0 (patent grant, common for security tooling) vs MIT. No LICENSE
      committed yet; decide before the repo goes public.
- [ ] Name for the chat/thread container concept (the Analyst works in threads — call them
      "investigations", "cases", or keep "threads"?).

## Setup / environment

- [ ] Install Wails CLI (needed for the desktop shell in P0).
- [ ] Create the `opensecbench` GitHub org + private repo; wire the remote and push.

## Deferred subsystems (tracked in the roadmap, not yet built)

- [ ] Remote outbound-connect runner + split `opensecbench-runner` repo (P12).
- [ ] Community extension hub with publisher verification / signing / scanning (P12).
- [ ] Hosted team service (only if existing-platform sharing proves insufficient).
- [ ] RDP/VNC remote-desktop sessions (P12); terminal (SSH/PTY) first in P7.
- [ ] RAG index for large corpora (P12); tool-based read/grep retrieval first.
- [ ] DOCX report export (P12); MD/HTML + PDF first.
