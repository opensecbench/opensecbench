# ADR-0052 — Provider connections, model discovery & the catalog as overlay

Status: Proposed. Split the fused **provider = type + one model + credential** record into three separable
concepts — **connection** (how to reach an inference API), **model** (an id that connection can serve), and
**model metadata** (price / context / routing tags, keyed by model *family*, connection-independent). Models
for a connection are **discovered live** from the backend and **enriched** by a curated overlay. This lets a
single connection expose *many* models — essential for gateway providers like **AWS Bedrock** and **Azure AI
Foundry**, which serve dozens of models across families behind one credential — and stops the model picker
from going stale.

Supersedes the catalog/routing mechanics of **ADR-0021 §3–§4**; extends ADR-0006 (provider adapters) and
ADR-0017/0019 (a profile/step's model is tag-resolved).

## Context

Today a provider is one row — `providers(id, name, type, model, base_url, key_sealed)`
(`migrations/global/0001_init.sql`, `pkg/store/providers.go`) — where `type` doubles as both the *protocol
adapter* (`pkg/llm/config.go` switch) and, implicitly, the *model family*. Three problems fall out of that
fusion:

1. **One connection = one model.** To run Opus *and* Sonnet from a single Anthropic key you create
   near-duplicate rows sharing the credential, or lean on the `model_routing` override — a second concept
   layered on the first (`pkg/analyst/service.go` `targetForTag`). This is the friction users hit.
2. **Gateways can't be expressed.** Bedrock and Foundry serve Anthropic + Llama + Mistral + DeepSeek + … all
   behind one endpoint/credential. A schema where `type` implies the family literally cannot represent "one
   connection, many families."
3. **The catalog goes stale.** The model list is a hand-curated `go:embed`ed file
   (`pkg/llm/catalog/models.json`) — ADR-0021 §3 justified this with *"We can't pull a provider's live model
   list."* That premise is now false: every OpenAI-compatible backend (OpenAI, DeepSeek, xAI, Ollama) exposes
   `GET /v1/models`, Anthropic exposes `GET /v1/models`, Bedrock exposes `ListFoundationModels` /
   `ListInferenceProfiles`, Foundry exposes a deployments/catalog API. The file today lists `grok-2-latest`,
   `deepseek-chat`, `gpt-4o`/`o3-mini` — all behind.

## Decision

### 1. Connection ≠ model ≠ metadata

Three concepts, separately stored:

- **Connection** — a protocol adapter + endpoint + credential (+ label, + adapter-specific config such as an
  AWS region or an Azure resource). Types are *protocols*, not families: `anthropic`, `openai` (OpenAI-
  compatible: OpenAI/DeepSeek/xAI/Ollama/…), `bedrock`, `azure-foundry`, `claude-cli`, `mock`. A connection
  carries **no** welded model.
- **Model** — an id a connection can serve (`claude-sonnet-4-5`, `meta.llama-3.3-70b`,
  `anthropic.claude-sonnet-4-5-20250929-v1:0`). Discovered per connection (§2), cached, never authoritative
  in code.
- **Model metadata** — `{ family, display_name, context_window, input_per_mtok, output_per_mtok,
  default_tags }`, **keyed by family / id-pattern**, connection-independent (§3).

### 2. Model discovery — a `ModelLister` per connection type

Add a capability to the provider layer:

```go
// ModelLister enumerates the models a connection can serve. Implemented per connection type.
type ModelLister interface {
    ListModels(ctx context.Context) ([]DiscoveredModel, error)
}
type DiscoveredModel struct { ID, DisplayName, Family string } // raw, pre-enrichment
```

Implementations: `openai` → `GET /v1/models`; `anthropic` → `GET /v1/models`; `ollama` → `/api/tags`
(or its `/v1/models` shim); `bedrock` → `ListFoundationModels` + `ListInferenceProfiles` (AWS SDK, SigV4);
`azure-foundry` → the model-catalog/deployments API; `claude-cli` → a fixed small set (no discovery
endpoint); `mock` → static. A connection whose type has no lister (or whose fetch fails) falls back to the
overlay's models for its inferred families, plus any user-pinned custom ids.

Discovery is **cached per connection** with a `last_refreshed` timestamp (a `connection_models` table:
`connection_id, model_id, display_name, family, …enriched fields…, last_seen`). New endpoint
`POST /v1/connections/{id}/models/refresh` re-pulls; `GET /v1/connections/{id}/models` returns the enriched,
cached set. The refresh is best-effort and offline-tolerant.

### 3. The catalog becomes a family-keyed overlay

`pkg/llm/catalog/models.json` stops being *the list* and becomes an **enrichment overlay**: entries keyed by
`family` (and optional id-pattern) supplying the metadata discovery can't return (price, context, tags). A
**normalizer** maps a served id to a family — so Anthropic's `claude-sonnet-5` and Bedrock's
`anthropic.claude-sonnet-4-5-20250929-v1:0` resolve to the *same* metadata record. Enrichment = `discovered
models ⋈ overlay-by-family`. The overlay is still `go:embed`ed, editable without code, and is the sole source
of models when a connection can't be reached.

### 4. Selection & routing point at `(connection, model)`

The `model_routing` map already stores `tag → { provider_id, model }` — keep the shape, rename to
`connection_id`, and feed its model picker from the connection's *discovered+enriched* set instead of the
static catalog. The single welded `model` on a connection is dropped as an authority; a connection may keep
an optional **default model** (used when a task names no tag). "Active provider" becomes "active connection +
default model." Cost-aware tag routing (`cheap`/`reasoning`/…) is unchanged in spirit — it just resolves
against real, current models with real prices.

### 5. Bedrock & Foundry adapters (first-class this pass)

- **Bedrock** — connection config `{ region, credential }` (access-key/secret or a profile/role); auth via
  AWS SigV4. Discovery via `ListFoundationModels` + `ListInferenceProfiles`; inference via the Bedrock
  **Converse** API mapped onto the `Provider`/tool-use contract (ADR-0017). Classified non-local for egress
  policy (ADR-0006/0011).
- **Azure AI Foundry** — connection config `{ endpoint, credential, (deployment map) }`; discovery via the
  Foundry catalog/deployments API; inference via the Azure AI Inference / Azure-OpenAI-shaped endpoint.

Both compose with the existing DLP guard and the remote-runner egress vantage (ADR-0025) unchanged, since
they enter through the same `Provider` boundary.

### 6. UI — two sections, as the mental model demands

Split today's single "Models & Providers" panel (`frontend/src/Providers.tsx`) into:

- **Connections** — add/test/delete credentials + endpoints (per-type fields: key, base URL, region, …). A
  **Refresh models** action + "last refreshed" per connection.
- **Models** — browse each connection's discovered+enriched models (family, context, price, tags), pin
  favorites, and set the default. Model Routing (`frontend/src/ModelRouting.tsx`) references
  `(connection, model)` and draws its picker from this set.

### 7. Migration & back-compat

The `providers` table is extended in place (add `connection`-oriented columns / a `connection_models`
companion table) rather than renamed hard, to keep stored rows valid. An existing `providers.model` becomes
that connection's **default model**; existing `model_routing` entries keep resolving (`provider_id` reads as
`connection_id`); usage attribution (`UsageByModel`) is unaffected. No user reconfiguration required.

## Consequences

- **The core fix:** one credential → all of a provider's models; gateways (Bedrock/Foundry) are finally
  expressible; the picker reflects what the backend actually serves *today*.
- **Never stale by construction.** New models appear on Refresh with no release. The curated file shrinks to
  metadata the APIs omit and to the offline fallback.
- **Adapters grow a second method.** Adding a backend is now `Provider` + (optionally) `ModelLister`; a
  backend with no discovery endpoint degrades to overlay + custom ids.
- **Egress/DLP unchanged.** Every backend still enters through the `Provider` boundary where policy attaches
  (ADR-0006/0011/0025).
- **Build order:**
  1. Schema: `connection_models` companion + connection columns; back-compat read of existing rows (§7).
  2. `ModelLister` interface + `openai`/`anthropic`/`ollama` listers; catalog → family-keyed overlay +
     normalizer + enrichment join (§2, §3).
  3. API: `GET /v1/connections/{id}/models`, `POST …/refresh`; routing picker off the enriched set (§4).
  4. Frontend: split into **Connections** + **Models** sections; wire Refresh (§6).
  5. **Bedrock** adapter (SigV4, Converse, discovery) (§5).
  6. **Azure AI Foundry** adapter (§5).
  7. Refresh `models.json` overlay to current families (Grok 4, DeepSeek V3.2, current OpenAI) — can land
     first, independently, to kill staleness immediately.
- **Out of scope now:** Vertex/Gemini adapter; per-connection rate/quota config; automatic periodic refresh
  (manual Refresh only for v1).

Composes with ADR-0021 (settings surface/sections — Connections/Models are custom sections), ADR-0017
(native tool-use per adapter), ADR-0019 (profiles carry a routing tag), and ADR-0025 (runner egress vantage).
