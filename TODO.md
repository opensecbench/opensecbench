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

## Proxy — make it a first-class tool (not an add-on)

The Proxy tab is currently minimal (a live-polled list of captured exchanges). It's meant to be a
primary daily-driver, so build it out. **Structure per ADR-0016**: Proxy/Replay/Intercept are one HTTP
toolset over a shared exchange substrate, built around four extension seams (exchange **actions**, proxy
**traffic processors**, history **columns/filters**, tools-as-**surfaces**) so features slot in cleanly
and can later ship as plugins.

- [x] **Filterable history UI** (Step 1) — master-detail table with method/status/URL filters, status
      badges, selection preserved across the live poll. *Still to add: column sort, host-parsed filter.*
- [x] **Request/response detail view** (Step 1) — full headers + body per exchange. *Still to add:
      JSON/HTML pretty-print + syntax highlighting.*
- [x] **Send to Replay** (Step 1) — one click from a captured exchange → a Replay document seeded with
      the request (via the shared action registry + the doc-seed model).
- [x] **Save-as-evidence** from a captured exchange (Step 1; registry action).
- [x] **Live push** (Step 2) — SSE stream (`GET /v1/projects/{id}/events`) over a generic in-process
      event hub (`pkg/events`); Proxy dropped the 2.5s poll (captures appear in ~15ms). The hub is the
      reusable foundation for future task/approval/Analyst streaming.
- [x] **Intercept & edit** (Step 3) — hold requests *and* responses, edit, forward/drop. Blocking
      `Interceptor` hook in `pkg/proxy` (both HTTP + TLS paths); in-memory hold queue in `pkg/api`
      (resolve/drain/ctx-cancel, all tested); control endpoints + audit; live over the SSE hub; an
      Intercept workbench surface. Verified E2E (edited body reached the upstream; drop blocked it).
- [x] **Match/replace + scope highlighting** (Step 4) — a persisted match/replace traffic-processor
      (regex rules over url/req+resp headers/body, live-applied by the running proxy) and in/out-of-scope
      row highlighting in history. The HTTP toolset (ADR-0016) is now fully delivered.
- [x] Server-side filtered `GET /v1/projects/{id}/exchanges` (Step 1; `origin/method/status/q/limit`).
      *Cursor pagination still to add; live push is Step 2.*

## Analyst provider / model management

- [x] **Provider display + selection** (DONE) — the Analyst header shows the active model (or "⚠ not
      configured" for mock); a provider settings panel lists/activates/tests/deletes and adds providers.
      Persisted `providers` entity (migration 0024) with vault-sealed keys; runtime swap + restore on start;
      a **test** round-trip catches a broken backend in one click (proved with the real claude CLI).
      *Follow-ups: per-provider allowed-sensitivity wired into egress routing; edit an existing provider.*
- [x] **`claude-cli` provider adapter FIXED** (2026-07-18): `claude` (Claude Code) IS usable as a completion
      backend via `-p --output-format json` — James does this to use a Claude subscription. The old adapter
      flattened our system prompt into `[SYSTEM]` user-text, which Claude Code rightly rejected as injection.
      Now: system prompt via `--append-system-prompt`; conversation on **stdin**; `--output-format json`
      parsed for `.result`; the CLI's own tools disabled (`--disallowed-tools`) so it only generates text.
      Verified against the real CLI. It's a first-class personal option (alongside `anthropic`/OpenAI/`ollama`).
- [ ] **Run `claude-cli` inside the runner** (governed setup) — mount only `~/.claude/.credentials.json`
      (read-only), NOT the whole `~/.claude`; the LLM-runner needs network egress to the provider (policy-
      gated) + an image with `claude`+node. Today the adapter execs on the host. James's home setup uses the
      subscription this way; API keys remain the recommended default for others.

## Small / medium follow-ups

- [ ] Broaden audit coverage to remaining entity CRUD (project/app/asset/finding create/update).
- [ ] Decide fail-closed vs best-effort on an audit-append failure (currently logged, not fatal).
- [ ] Interpret TruffleHog JSON output into observations (currently captured as a raw artifact).
- [x] Report templates as extension packs; playbook/visualization pack types still to add.
- [ ] Integration **pull** (DefectDojo findings/scans in; DependencyTrack SBOM) + **watchers**
      (schedule → notify / create task / run playbook).
- [x] Interactive graph tab: structure, traffic, topology (nmap), dependency (SBOM) kinds
- [ ] Runtime extension **uninstall** + update/version-constraint flow.

## Plugin / extension ecosystem

- [ ] **HTTP traffic-tool plugin system** (ADR-0016) — expose the toolset's in-tree extension seams
      (exchange **actions**, proxy **traffic processors**, history **columns/filters**, and whole **tools**
      as surfaces) as **signed extension packages** (ADR-0003 format, ADR-0013 loader), governed by the
      same publisher-trust + sandbox model as capabilities/methodologies. Our vendor-neutral analog to
      proxy-suite plugin ecosystems. Build the seams as registries now; package them as plugins later.
- [ ] Generalize beyond HTTP: let extension packages contribute new **workbench surfaces/tools** in
      general (the HTTP toolset is the first proving ground).

## Large subsystems (own effort each)

- [ ] **Remote outbound-connect runner** + split `opensecbench-runner` repo — a runner agent that
      dials home and executes tasks over the runner protocol (ADR-0004 is additive-ready).
  - [ ] **Runners as request-egress endpoints** (builds on the above + ADR-0016) — route Proxy/Replay
        (and network capabilities') outbound requests *through a selected remote runner* instead of the
        local host. Unlocks **distributed/geo-distributed testing** (observe a target from different
        regions/source IPs) and **origination from inside the target's network** (a runner deployed in
        their environment) — high-value for internal assessments and egress/segmentation testing. Needs a
        per-tool/per-request "egress via runner X" selector; scope guard + DLP + audit still governed by the
        control plane (the runner is only the exit point). Idea from James, 2026-07-18.
- [ ] **Hosted team hub service** — accounts, uploads, submission scanning, reputation, moderation
      (the static-index hub + signed format is the foundation).
- [ ] **RDP/VNC** interactive sessions (terminal shipped in P7).
- [ ] **RAG index** for large corpora (tool-based read/grep retrieval today).
- [ ] **SSH-to-external-host** terminal + Analyst co-drive of terminals (sandboxed container terminal
      shipped; scope + DLP + audit already apply).
- [ ] **KMS / OS-keychain** vault key custody (env var or 0600 key file today).
- [ ] Hosted team **collaboration/sync** (portable export/import + webhook sharing today).
