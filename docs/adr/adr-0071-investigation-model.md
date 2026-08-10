# ADR-0071 — Investigation model: assets, entity graph, research state & operator tooling

Status: Accepted. OSB gains an investigation-first data model — new asset types (domain, host,
endpoint), a generic entity-link graph for topology and evidence chains, a research-state primitive
for investigator notes/hypotheses/experiments, and a pentest workstation with bridge networking —
so that external-target workflows (bug bounty, pentesting, CTF, red team) are first-class alongside
source-code analysis, and the investigation itself lives in OSB rather than in the operator's head.

## Context

OSB was built source-code-first: create a project, point it at a repo, run SAST/SCA, triage
findings. Bug bounty and pentest workflows start from the opposite end — a scope document listing
in-scope domains and APIs, then external recon, asset discovery, and manual testing against live
targets. The primitives are mostly there — proxy (ADR-0016), replay, KB, agent toolset (ADR-0017),
engagement record (ADR-0051), scope entries (ADR-0001), web_service target (ADR-0067), dispositions
(ADR-0028) — but the workflow isn't first-class. Specific gaps:

1. **Asset vocabulary.** The only external asset type is `web_service` (ADR-0067). There is no
   `domain`, `host`, or `endpoint` type, so recon output (subdomains, resolved IPs, discovered
   routes) has nowhere to land in the model.

2. **Relationships.** Assets have no graph structure. A subdomain discovered by subfinder, resolved
   by dnsx, probed by httpx, and tested through the proxy generates four isolated records with no
   topology connecting them. Parent-ID trees are too rigid — real topology is a DAG (CNAME chains,
   multi-homed hosts, shared endpoints).

3. **Research state.** The triage pipeline (observations → findings) tracks tool output, not
   investigator reasoning. There is no model for "I noticed X, hypothesized Y, tested Z, concluded
   W." The investigation lives in the operator's memory or a text file.

4. **Engagement kinds.** The `KINDS` selector in the engagement modal (ADR-0051) forces a
   project-level declaration of assessment type (pentest, code review, etc.) and surface (REST,
   GraphQL, etc.). In practice, surfaces emerge through asset discovery and tagging, not upfront
   declaration. The distinction between pentest and bug bounty is in constraints (scope, auth,
   timeline), not project type.

5. **Operator shell.** The sandboxed `run_code` environment (alpine, no network) is adequate for
   one-off commands but not for sustained investigation scripting with Python, nmap, and security
   tooling. Operators escape to their normal terminal, losing the instrumented feedback loop.

6. **Discovery provenance.** An asset created by subfinder and an asset confirmed by a human have
   the same authority. Origin and verification state are conflated — or, more precisely, neither
   exists.

The architectural shift is larger than "adding bug bounty support." OSB is evolving from a workbench
for analyzing applications into a workbench for conducting application security investigations. That
aligns proxy, KB, source analysis, live testing, agents, findings, and recon tooling around a single
investigation model.

## Decision

### 1. Engagement rework — constraints, not kinds

Remove the `KINDS` array from the engagement modal (ADR-0051). The engagement captures *constraints*
only: scope, authorization, timeline, rules of engagement, contacts, test accounts. Surfaces (REST,
GraphQL, WebSocket, etc.) are asset-level tags, not project-level declarations.

Add three fields to the engagement record:
- `ProgramURL` — bug bounty program page
- `Platform` — `hackerone` | `bugcrowd` | `intigriti` | `independent` | empty
- `ScopeDocumentRef` — CAS artifact ID of the imported scope document

The `Kinds` column stays in the schema for backward compatibility but is no longer read or written.

### 2. Asset model — types, provenance, tags

**New asset types:**

| Type | Example | Purpose |
|---|---|---|
| `domain` | `api.example.com` | DNS namespace object |
| `host` | `93.184.216.34` | Network identity (IP) |
| `endpoint` | `POST /v2/users` | Application route |
| `service` _(deferred)_ | `93.184.216.34:443/tcp` | Protocol listener |

Existing types `web_service` (ADR-0067) and `source_repo` are unchanged.

**Origin and verification state** are separate axes on every asset:

- Origin (how it entered): `manual` | `tool` | `agent` | `proxy` | `import`
- Verification state (epistemic confidence): `unverified` | `observed` | `corroborated` | `verified` | `disputed`

A subfinder discovery is `origin=tool, verification_state=unverified`. A proxy-observed endpoint is
`origin=proxy, verification_state=observed`. A human confirms: promotes to `verified`. This
distinction is critical once agents create knowledge.

**Tags** — curated registry with autocomplete, plus freeform custom tags. Curated categories:
surface (`rest-api`, `graphql`, `websocket`), auth (`authenticated`, `oauth`, `jwt`), infra
(`cdn-fronted`, `waf-detected`), state (`admin-panel`, `file-upload`). Tags drive methodology
binding (see §10).

**Status** lifecycle: `discovered` → `confirmed` → `investigating` → `tested`. Default: `confirmed`
for manually created assets, `discovered` for auto-populated.

**Metadata** — flexible `map[string]string` for IPs, ports, technologies, service versions, OS
fingerprint.

### 3. Entity links — generic graph primitive

A single `entity_links` table for all inter-entity relationships — asset topology, research chains,
evidence, and cross-cutting links:

```sql
CREATE TABLE entity_links (
    id TEXT PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_id TEXT NOT NULL,
    relationship TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    note TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Indexed on `(source_type, source_id)` and `(target_type, target_id)`.

Entity types: `asset`, `research_item`, `finding`, `observation`, `kb_entry`, `exchange`.

**Intended asset topology** (defining the chain now; `service` deferred):

```
domain ──resolves_to──▶ host
domain ──cname──▶ domain
host ──exposes──▶ service
service ──serves──▶ web_service
web_service ──contains──▶ endpoint
```

Until `service` is implemented, `host ──serves──▶ web_service` is the shortcut.

Additional relationship types: `redirects_to`, `discovered_from`, `same_application`,
`depends_on`, `derived_from`, `tests`, `supported_by`, `evidence`, `about`, `escalated_to`,
`affects`.

### 4. Research items — investigation state

A new `research_items` table models investigator reasoning, distinct from triage observations
(ADR-0028):

- **Types**: `note` | `hypothesis` | `lead` | `question` | `experiment` | `result` | `conclusion`
- **Fields**: `id`, `project_id`, `type`, `title`, `body` (optional), `status` (open/active/resolved/discarded), `assessment` (optional — low/medium/high/confirmed), `created_by`, `tags`, timestamps
- **Linked via entity_links**: `hypothesis →derived_from→ note`, `experiment →tests→ hypothesis`, `conclusion →escalated_to→ finding`, `note →evidence→ exchange`, `note →about→ asset`

The `note` type is used instead of `observation` to avoid collision with `model.Observation` in the
triage pipeline. Research items link TO triage observations and findings via entity_links but are a
separate model.

**Quick capture** is the critical UX decision: a persistent input accepts one line of text, creates a
`note` with that text as the title, auto-links the current asset context, and timestamps it.
Everything else (body, assessment, tags, explicit links) is enrichable after the fact. If recording
research takes a form with seven fields, operators will use a text file instead.

### 5. URL scope kind

Add `KindURL = "url"` to `pkg/scope` (ADR-0001). Value format: `https://api.example.com/v2/*`.
Matching: scheme must match if specified; host matches using domain-style subdomain matching; path
is prefix-match with trailing `*` wildcard.

The proxy's `projectAllows` passes bare hostnames, so URL entries degrade to host-only matching
there (correct: the proxy gates CONNECT, not paths). Path-level enforcement applies at the
replay/analyst scope-check layer where full URLs are available.

Fix `inferKind` in the frontend: `value.includes('/')` currently returns `cidr` for URLs. Add a
`https?://` check first. Normalize `*.example.com` entries by stripping `*.` since `KindDomain`
already matches all subdomains.

### 6. Scope document import

New endpoint `POST /v1/projects/{id}/scope/import` — accepts `{text, source_url?}`, returns
extracted `ScopeSeed[]` for user review before adding.

Two-pass: a deterministic parser extracts URLs, wildcard domains, CIDRs, IPs, and detects
in-scope/out-of-scope sections by heading keywords. An agent fallback triggers on *complexity
signals* — low confidence, exclusion language that couldn't be structured, technique restrictions,
tables/unusual formatting, ambiguity — not entry count. A program with one target and complicated
prose deserves the agent; a program with 200 clean domain lines does not.

The original scope document is preserved as a CAS artifact and linked to the structured rules OSB
derives from it. Parsed entries never replace the original language.

### 7. Granular technique model

Capabilities declare their *effects*: `network_connect`, `enumeration`, `brute_force`,
`authentication_attempt`, `content_submission`, `state_change`, `destructive`, `high_volume`.

Rules of engagement allow or deny effects. The engine checks that a capability's declared effects
are all permitted before execution. A capability like ffuf declares `[enumeration, brute_force,
high_volume]`; the engine blocks it unless all three are allowed. Existing technique strings map
into the new vocabulary.

### 8. Pentest workstation & runtime policy

A rich Docker image (`images/pentest-workstation/`) with Python, nmap, httpx, subfinder, nuclei,
dnsx, ffuf, sqlmap, wordlists, and general-purpose scripting tools. Multi-arch, published to GHCR,
overridable via `OSB_WORKSTATION_IMAGE`.

A bundled `osb` CLI helper (statically linked Go binary) feeds results back to the OSB model via
the control-plane API: `osb note`, `osb asset add`, `osb tag`, `osb link`, `osb status`. The API
token and base URL are injected as environment variables.

**Runtime policy is explicit per-project**, not inferred from assets. Two new engagement fields:
`RuntimeImage` (session image, default empty = alpine:3) and `RuntimeNetwork` (network mode,
default empty = none). If network-relevant assets exist, OSB *recommends* the workstation and bridge
networking — the operator confirms once, and the policy persists. No silent policy changes.

### 9. Proxy-driven asset discovery

The proxy's `onExchange` callback feeds the asset model during manual testing:
- Extract host → upsert `web_service` (origin=proxy, verification_state=observed)
- Extract path → upsert `endpoint` (origin=proxy, verification_state=observed)
- Create entity_links: web_service →contains→ endpoint
- Auto-tag from response headers (Server, X-Powered-By, Content-Type)

Throttled/batched to avoid overwhelming the store during heavy browsing. Dedup via unique index.
Only processes in-scope exchanges (the proxy already scope-checks via `projectAllows`).

### 10. Conservative discovery chaining

Auto-chain only passive operations: subfinder → DNS validation → HTTP probing. Active operations
become proposed actions using the existing playbook gate mechanism:

> "42 new hosts discovered. Nmap is allowed under the current RoE. Run service discovery?"

The operator approves. Projects can later opt into automatic execution of approved active
techniques.

### 11. Methodology binding — advisory, per-asset

Methodology packs declare `AppliesTo` (asset types) and `AppliesTags` (tag matches). When an asset
is created or tagged, matching methodology items are *suggested*, not required. "Applicable but
intentionally skipped because X" is valid engagement evidence. Methodology coverage is
informational.

### 12. Default application

Auto-create a default application when a project is created (name = project name or "Targets").
Discovered assets go into this application. The Application layer remains structural (wired into
schema/store/API) but becomes transparent for investigation workflows — the UI hides the grouping
when there's only one. Code-review workflows can still create multiple applications.

## Consequences

- Investigation-first workflows become first-class: scope doc → asset inventory → research → findings
  is a supported path, not an afterthought bolted onto source-code analysis.
- The entity-link graph is powerful but requires query discipline — naive traversal of a large graph
  will be slow. Index coverage and bounded-depth queries are the mitigation.
- The `service` type (port+protocol on a host) is deferred. The intended topology is defined now so
  that `host ──serves──▶ web_service` can later be split into `host ──exposes──▶ service ──serves──▶
  web_service` without restructuring the graph.
- The infrastructure graph UI (table/tree, then interactive graph) is a later phase. The data model
  lands first; the visualization follows.
- Research items and triage observations are separate models with different lifecycles. They connect
  via entity_links (`conclusion →escalated_to→ finding`, `note →about→ observation`), not by
  inheritance.
- The pentest workstation image is a maintenance surface — security tools update frequently, and the
  image must track upstream releases. Multi-arch builds add CI cost.
- Quick capture is the highest-leverage UX investment. If it's too slow or too structured, operators
  will not use it, and the research model becomes dead schema.
- Amends ADR-0051 (engagement modal rework), ADR-0001 (URL scope kind), ADR-0067 (asset vocabulary
  expansion), ADR-0017 (agent asset/research tools).
