# TODO backlog

Future work and deferred decisions. **Rule: never stub something out without adding a TODO here**
(and a matching `// TODO:` in code). Items graduate into [TASKS.md](TASKS.md) when we pick them up.
Roadmap phases P0–P12 are substantially delivered; what remains is reach, polish, and a few large
infrastructure pieces that warrant their own focused efforts.

## Decisions to confirm

- [x] **OSS license** — Apache-2.0 (patent grant, common for security tooling). `LICENSE` committed.
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

## Analyst autonomy

- [x] **Knowledge capture loop** (ADR-0040) — a `knowledge-scribe` profile distills what a run discovers
      (analysis notes, observations/findings, corpus) into durable KB drafts (architecture/auth/tech_stack/
      data_flow/conventions/gotchas); `list_kb` tool to see + dedupe existing knowledge; capture wired into
      the playbooks (`capture-knowledge` + an onboarding capture step). Drafts human-confirmed, carry across
      engagements on the same target.
- [x] **Org/team-level knowledge scope** (ADR-0041) — KB anchors above a target (org/group/global);
      migration 0043 recreates `kb_entries` with nullable target/group/org anchors + scope CHECK;
      `ListKBByProject` inherits target + group + org + global (most-specific first); `draft_kb_entry` gains
      a `scope` param (resolves the org from the project); the scribe captures org-wide facts at org scope.
      *Remaining: human API/UI for org/global entries; a **group** CRUD API; RAG index inherited entries
      per-org not per-project.*
- [x] **Knowledge dossier** (ADR-0042) — a consolidated "what we know about this system" view: `pkg/dossier`
      deterministically groups the inherited KB by kind into a read-first brief (architecture → stack → auth
      → deploy → data flows → conventions → gotchas), marking inherited/draft entries. `get_dossier` agent
      tool + `GET /v1/{targets,projects}/{id}/dossier` (+`?format=markdown`) + `osb dossier`. No model call.
      *Also later: optional LLM-narrative summary; a workbench UI surface; a "current posture" section from
      recent findings.*
- [x] **Knowledge freshness** (ADR-0043) — the last knowledge investment. KB entries carry a `last_verified_at`
      (set on human create + on confirm); confirmed facts go **stale** past a kind-specific window
      (architecture 1yr, auth/data 180d, endpoints/env 90d) so knowledge doesn't silently rot. Migration 0044.
      `verify_kb_entry` agent tool + `POST /v1/kb/{id}/verify` + `osb kb verify` bump freshness **without**
      confirming drafts (human gate preserved). Dossier orders fresh→stale→draft and tags stale entries;
      `list_kb` returns a `stale` flag. Closes the loop: capture → scope → consolidate → keep current.
      *Also later: auto-expiry/hiding of long-stale entries; a workbench staleness nudge; a scheduled
      re-confirm playbook; per-entry window overrides; freshness-weighted RAG ranking.*

- [x] **Tech-scout research agent** (ADR-0038) — identifies the stack (`list_dependencies` from the SBOM),
      researches it from trusted web sources (`web_fetch`, gated by a **preapproved-source** allowlist —
      NVD/OSV/GHSA/MITRE/OWASP/CIS auto, anything else needs approval), and drafts gotchas/hardening into the
      KB + corpus (`save_context`, the RAG precursor). Fetched content wrapped as untrusted. `tech-scout`
      profile + playbook. Validated live (fetched OSV auto; example.com paused for approval). *Remaining:
      `web_search`; a configurable `research_sources` setting (default allowlist ships in code); then the
      **RAG index** over the corpus this fills.*

- [x] **Signal-aware autonomous assessment** (ADR-0035) — the Analyst drives recon → scan → triage →
      validate → draft-report end-to-end as a bounded run that **proposes** (findings stay human-confirmed).
      New `list_observations`/`list_investigations` tools surface the routing attributes; the investigation
      seed carries them (`describeSignals`); an `assessor` profile (no `create_finding`) + an `assessment`
      playbook enforce propose-mode by construction.
- [x] **Mid-run plan approval** (ADR-0044) — a plan can **pause for human approval** at a declared **gate**
      step and **resume** where it left off. `runPlan` reconstructs progress from persisted step state
      (resume-safe across daemon restarts); a gate parks the plan `waiting` + raises a notification;
      `ResolvePlanGate` approves (clears gate) or denies (skips → plan fails) and relaunches. Migration 0045
      (`gate`/`gate_approved`). `POST /v1/plans/{id}/steps/{stepID}/resolve` + `osb plan start|get|list|
      approve|deny`. The `assessment` playbook gains an `approve-validation` gate before PoCs/traffic.
      Validated live (pause→approve→done; deny→skip→failed; notification fired). *Remaining: edit-while-paused;
      a distinct `cancelled` status; a workbench UI to approve/deny inline; gate timeout/auto-deny.*
- [x] **`generate_report` agent tool** (ADR-0045) — the Analyst compiles the project's **confirmed findings**
      into a durable, downloadable report deliverable, reusing the report builder/templates/CAS (ADR-0008).
      Evidence-bound (findings only — can't fabricate); `md` default (no browser) / `html`; on the
      `report-writer` + `pentester` profiles (post-confirmation roles), deliberately NOT in the propose-only
      assessment playbook. Closes the loop: assess → human confirms findings → report. *Later: PDF/DOCX from
      the agent; a preview (render-without-store) tool; agent-attached executive narrative on top.*
- [x] **Parallel plan steps** (ADR-0046) — the plan runner runs **all ready steps concurrently** per wave
      (bounded by `maxParallelSteps`=4) instead of one at a time, so independent steps (SAST/SCA/secrets
      scanners, per-repo recon) overlap — wall-clock = slowest branch, not the sum. Shared state folded under
      a mutex (race-clean via `go test -race`); ADR-0044 gate pause/resume + failure-skip preserved. Fan-out
      test proves real concurrency. Live-validated (root→{a,b,c}→join completed through the daemon).
- [x] **Deeper delegation + configurable sub-agent budget** (ADR-0047) — delegation can **nest more than one
      level**: the `pentester` coordinator can itself delegate a separable sub-task, bounded by
      `maxDelegationDepth` (default 3, `OSB_AGENT_MAX_DEPTH`) tracked on the context so the tree stays finite.
      `delegate` stays sensitive/approval-gated and only on coordinator roles. The delegated sub-agent's
      tool-turn budget is raised 8→16 and configurable (`OSB_AGENT_MAX_STEPS`). Depth-cap + increment tested.
- [x] **Assessment scanner fan-out** (ADR-0048) — the `assessment` playbook's single `scan` step is split into
      four parallel per-scanner steps (`scan-sast`/`scan-sca-grype`/`scan-sca-govulncheck`/`scan-secrets`),
      each depending only on `recon`; `triage` depends on all four. The plan runner (ADR-0046) runs them as one
      concurrent wave — scan phase takes the slowest scanner's time, not the sum; a failed/skipped scanner
      skips only itself. Test drives the real playbook and asserts the wave runs concurrently then gates.
- [ ] **Analyst autonomy — remaining** (ADR-0035): a **pipelined** scheduler (start a step the moment its own
      deps finish, dropping the per-wave barrier); a configurable/per-playbook concurrency cap; cancel
      in-flight steps on a sibling failure; a delegation **trace** in the UI; per-asset scanner fan-out for
      multi-repo projects; conditional steps (skip a scanner step when the KB shows no relevant assets).

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
- [x] **Run `claude-cli` inside the runner** (governed setup, ADR-0018) — `CLIProvider.Sandbox` runs the CLI
      in a runner container mounting ONLY `~/.claude/.credentials.json` (read-only) at `$HOME/.claude/`, with
      an egress network (default `bridge`) so it can reach the API. Opt-in via `OSB_LLM_CLI_SANDBOX=1` +
      `OSB_LLM_CLI_IMAGE` (an image with `claude`+node; operator-provided); host exec stays the default.
      `RunSpec` gained `Stdin`. Isolation asserted via a fake-runner test; stdin verified against a real
      container. **Remaining:** narrow egress to just the API host (rides on runners-as-egress-endpoints).

## Small / medium follow-ups

- [ ] Broaden audit coverage to remaining entity CRUD (project/app/asset/finding create/update).
- [ ] Decide fail-closed vs best-effort on an audit-append failure (currently logged, not fatal).
- [x] Interpret TruffleHog JSON output into observations (verified→high/unverified→medium; verified/detector attributes).
- [x] **Pluggable SAST engines** (ADR-0036) — **opengrep** (LGPL Semgrep fork, `osb/opengrep` image) is the
      default SAST engine: `--dataflow-traces` emits the SARIF `codeFlows` Semgrep CE masks, so ADR-0032
      reachability works free (validated in Docker; route-map also moved to it). **semgrep** kept for
      existing licenses (`pro=true` + `SEMGREP_APP_TOKEN` secret → Pro engine). *Remaining: build the
      **Checkmarx** capability (Checkmarx One `cx` CLI, credential-gated — needs a tenant to build/test);
      verify the Semgrep-Pro path against a real license; per-project default-engine setting; arm64 image.*
- [x] **Observation & investigation dedup polish** (ADR-0037, from live E2E testing) —
      `GET /v1/projects/{id}/observations` (+`osb obs list --project`); **refresh-on-rescan** (a deduped
      observation's severity/attributes update, preserving review_state, no re-dispose); **cross-tool vuln
      dedup** (`investigation_vulns`: same CVE via grype GHSA + govulncheck CVE → one investigation).
      *Remaining: cross-tool observation merge; re-route a refreshed obs that newly crosses a threshold.*
- [x] **Post-run disposition routing** (ADR-0028) — tool-declared rules (`pkg/disposition`, manifest +
      project overrides) route interpreted observations: auto-finding / investigation / review. Observations
      gained structured `attributes`; investigations are human-triggered vuln-validator threads (findings
      stay human-gated). TruffleHog: verified→finding, unverified→investigate. *Remaining: pre-run hooks;
      interpreter plugin registry.*
- [x] **Observation dedup + multi-tool routing** (ADR-0029) — observations gained a content `fingerprint`
      so a re-scan doesn't re-create/re-dispose a known finding (no duplicate observations, no repeated
      investigations / token spend). SARIF interpreter now carries `tool`/`security_severity` attributes +
      CVSS-refined severity; semgrep & grype declare high/critical→investigate. *Remaining: fuzzy/line-
      tolerant fingerprints; refresh a deduped observation on re-scan; SCA-specific grype interpreter.*
- [x] **Static reachability + exposed-service model** (ADR-0030, Phase 1) — real Go call-graph reachability
      (`govulncheck` capability + interpreter → `reachable` attribute) + a derived exposed-service signal
      (`store.ProjectExposure` from nmap open-ports / proxy exchanges / cloud_deployment+infra assets; engine
      enriches `exposed` on observations); govulncheck routes `reachable + exposed → investigate`. Reachable/
      exposed pills in the triage view. *Phase 2 remaining: SAST reachability (exposed handler → sink);
      correlate govulncheck reachability onto grype CVEs + parse the SBOM into tables; explicit/periodic
      exposure; multi-language reachability; a prebuilt govulncheck image.*
- [x] **Cross-tool reachability correlation** (ADR-0031, Phase 2a) — reachability verdicts stored per CVE
      (`reachability` table; `store.SetReachability`/`ReachabilityForCVE`), populated generically by any
      analyzer (govulncheck) and reused to enrich other tools' CVE findings. grype now routes
      `reachable:false→review` (downgrade even if high), `reachable+exposed→investigate`, else severity
      fallback. *Remaining: retroactive re-eval of existing findings when a verdict arrives; cross-tool
      observation merge for the same CVE.*
- [x] **SAST dataflow reachability** (ADR-0032) — a semgrep taint finding's SARIF `codeFlows` (source→sink)
      is read as `reachable=true` + `dataflow_source`; semgrep routes `reachable+exposed→investigate` (any
      severity) with a high-severity fallback. Free from semgrep's own taint engine; the `reachable` triage
      pill lights up for SAST too. *Remaining: framework-aware entry-point/route resolution (which exposed
      route reaches the source); interprocedural reachability beyond semgrep's taint engine.*
- [x] **Exposed route inventory + route-aware findings** (ADR-0033) — a `routes` table + `route-map`
      capability (bundled offline semgrep ruleset → `pkg/interpret/routes.go`) extracts declared HTTP routes;
      `store.ReconcileObservedRoutes` confirms them against captured traffic and records traffic-only routes
      (works with no source). A finding whose handler file declares an exposed route gets `exposed_route` +
      a 🌐 triage pill (file-level proximity). *Remaining: call-graph route→sink reachability; a routes
      graph/surface; broader framework coverage; ship the ruleset to remote runners; Flask methods=[…]
      inference.*
- [x] **Route-confirmed escalation** (ADR-0034) — `route_observed` is now a routing gate: a finding in a
      traffic-confirmed exposed route's handler escalates to investigate at medium+ even without a
      reachability proof (escalate-only, never downgrades on a missing route; grype's authoritative
      `reachable:false` still wins). *Remaining: escalate on merely-declared routes; call-graph route→sink.*
- [x] Report templates as extension packs; playbook/visualization pack types still to add.
- [x] Integration **pull** — DefectDojo findings → observations into triage (ADR-0027); reusable
      per-project configs (push uses them too); external-id dedup; `observations.project_id` for task-less
      evidence. *Remaining: DependencyTrack/SBOM; **watchers** (schedule → notify / create task / run
      playbook); DefectDojo pagination beyond 200; two-way status sync.*
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

- [x] **Remote outbound-connect runner** — Phase 1 delivered (ADR-0024): `osb-runner` agent dials home,
      ed25519-enrolled over a separate `--runner-addr` listener; `pkg/runnerhub` broker + engine
      runner-selection (`RunRequest.RunnerID` → durable `tasks.runner_target`); network capabilities run
      from the runner's vantage; scope enforced control-plane-side before dispatch. *Remaining:* source-scan
      caps on remote runners (ship `/src`), built-in TLS + cert-pinning, split `opensecbench-runner` repo,
      Runners UI.
  - [x] **Runners as request-egress endpoints — Phase 2a: Replay** (ADR-0025) — Replay send (UI +
        Analyst `send_request` tool) egresses from a chosen runner's vantage via an HTTP-request job on the
        runner protocol; shared `egressSend` selector; `egress` provenance recorded on the exchange; scope
        stays control-plane-side.
  - [x] **Phase 2b: proxy MITM egress via runner** (ADR-0026) — multiplexed, credit-flow-controlled
        streaming tunnel (`pkg/runnertunnel`) over one WebSocket per runner; the proxy forwards every
        request through a chosen runner with streaming responses; per-session egress + provenance; MITM
        decode/capture preserved. *Remaining: raw-TCP/CONNECT passthrough tunneling; a per-project default
        egress runner; unify the agent's SSE + WS connections.*
- [ ] **Hosted team hub service** — accounts, uploads, submission scanning, reputation, moderation
      (the static-index hub + signed format is the foundation).
- [ ] **RDP/VNC** interactive sessions (terminal shipped in P7).
- [x] **RAG index** for large corpora (ADR-0039) — semantic retrieval over the corpus + KB: chunk + embed +
      store vectors (`corpus_chunks`, BLOB + brute-force cosine in Go; modernc SQLite has no vec extension);
      `search_corpus` agent tool (+ `osb rag reindex|search`, `POST /reindex`, `GET /search-corpus`).
      Embeddings default **local** (ollama, `OSB_EMBED_BASE_URL`/`OSB_EMBED_MODEL`) so the corpus stays
      on-host; retrieval egress-gated like `read_context`; index-on-write + reindex. *Remaining: ANN for very
      large corpora; re-ranking; hybrid lexical+semantic; index findings/observations; a pure-Go embedder to
      drop the ollama dependency.*
- [ ] **SSH-to-external-host** terminal + Analyst co-drive of terminals (sandboxed container terminal
      shipped; scope + DLP + audit already apply).
- [ ] **KMS / OS-keychain** vault key custody (env var or 0600 key file today).
- [ ] Hosted team **collaboration/sync** (portable export/import + webhook sharing today).

## Functional gaps (2026-07-20 project review — untracked stubs/gaps)

- [x] **Finding lifecycle** — `store.SetFindingStatus` exists (open|confirmed|remediated|accepted) but has
      NO API route and NO UI, so findings are write-once. Add `POST /v1/findings/{id}/status` + a status
      control on finding rows. *(highest value, small)*
- [x] **Methodology un-adopt UI** — `unadoptMethodology` (store+API+client) exists but has no button; you can
      adopt a pack but never remove it.
- [ ] **Analyst can't read binary context** — `pkg/analyst/corpustools.go:82` returns a note, not content,
      for PDF/docx/binary docs; a doc/email-ingesting assessment tool should extract text.
- [x] **Swallowed persistence errors in orchestration** — `planrunner.go:87-206` discards every
      `UpdatePlanStep`/`UpdatePlanStatus`; `scheduler.go:54` swallows `markRun`. A DB failure mid-plan is
      invisible (wrong resume / schedule re-fire). Surface/handle these.
- [ ] **Interactive sessions are bare** — `session/docker.go:16` hardcoded image, no assessment toolchain /
      cloud CLIs baked; `:42` no scoped network.
- [ ] **Proxy TLS cert capture** — `proxy/proxy.go:69` captures no upstream cert chain/validity, so invalid/
      expired certs can't be flagged on an exchange.
- [ ] **API 400-vs-500 mapping** — user-input errors (e.g. createProject unknown target, `api.go:1577`)
      return 500 instead of 400.
- [x] **Cleanup: orphaned template-create flow (frontend)** — the engagement modal replaced it (+ archetype picker
      removed), leaving `listTemplates`/`createProjectFromTemplate`/`createProject` and the `/v1/templates`
      + from-template endpoints unused. Wire templates back into the modal or retire the endpoints. Also dead
      client methods: `getProject`, `getSession`, `getFinding`, `pickFile`.
