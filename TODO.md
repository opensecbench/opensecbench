# TODO backlog

Future work and deferred decisions. **Rule: never stub something out without adding a TODO here**
(and a matching `// TODO:` in code). Items graduate into [TASKS.md](TASKS.md) when we pick them up.
Roadmap phases P0–P12 are substantially delivered; what remains is reach, polish, and a few large
infrastructure pieces that warrant their own focused efforts.

## Decisions to confirm

- [ ] **OSS license** — Apache-2.0 (patent grant, common for security tooling) vs MIT. No LICENSE
      committed yet; decide before the repo goes public.
- [ ] Name for the chat/thread container concept (Analyst threads — "investigations"? keep "threads"?).

## Delivered (kept for provenance)

- [x] Append-only, hash-chained audit trail wired into the control plane; `osb audit --verify`
      recomputes the chain (tamper detection).
- [x] Encrypted vault + exec-time secret injection + output redaction; DLP egress monitor + canaries.
- [x] Multi-type reports (executive/technical/retest/compliance/branded) in MD/HTML/PDF/DOCX with
      inline-SVG figures.
- [x] Methodology & coverage model; KB-driven methodology applicability suggestions.
- [x] Knowledge base (durable, target-anchored, inherited); portable encrypted export/import + signing.
- [x] Extension loader (signed, digest-pinned, sandboxed) + community hub (static index, publish/
      browse/install with explicit trust).
- [x] Governance profiles (personal/corporate/strict); webhook (Teams/Slack) notifications.

## Desktop app

- [x] **Wails desktop build compiles + vets** with `CGO_ENABLED=1 go build -tags "desktop webkit2_41"`
      (webkit2gtk-4.1 2.52 + gtk3 present); `frontend/dist` embeds. The remaining step is *running*
      the GUI (`make dev`) on a desktop session — needs a display, so do it locally.
- [x] Desktop "Open browser" button in the Proxy tab (Wails binding → browser.Launch).

## Small / medium follow-ups

- [ ] Broaden audit coverage to remaining entity CRUD (project/app/asset/finding create/update).
- [ ] Decide fail-closed vs best-effort on an audit-append failure (currently logged, not fatal).
- [ ] Interpret TruffleHog JSON output into observations (currently captured as a raw artifact).
- [x] Report templates as extension packs; playbook/visualization pack types still to add.
- [ ] Integration **pull** (DefectDojo findings/scans in; DependencyTrack SBOM) + **watchers**
      (schedule → notify / create task / run playbook).
- [x] Interactive graph tab: structure, traffic, topology (nmap), dependency (SBOM) kinds
- [ ] Runtime extension **uninstall** + update/version-constraint flow.

## Large subsystems (own effort each)

- [ ] **Remote outbound-connect runner** + split `opensecbench-runner` repo — a runner agent that
      dials home and executes tasks over the runner protocol (ADR-0004 is additive-ready).
- [ ] **Hosted team hub service** — accounts, uploads, submission scanning, reputation, moderation
      (the static-index hub + signed format is the foundation).
- [ ] **RDP/VNC** interactive sessions (terminal shipped in P7).
- [ ] **RAG index** for large corpora (tool-based read/grep retrieval today).
- [ ] **SSH-to-external-host** terminal + Analyst co-drive of terminals (sandboxed container terminal
      shipped; scope + DLP + audit already apply).
- [ ] **KMS / OS-keychain** vault key custody (env var or 0600 key file today).
- [ ] Hosted team **collaboration/sync** (portable export/import + webhook sharing today).
