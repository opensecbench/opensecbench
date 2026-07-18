# ADR-0017 — First-class tool use & provider translation

Status: Accepted (design); implementation phased. Evolves ADR-0006 (agent runtime & providers).

## Context

The Analyst's tool-calling is bolted on, not first-class. The provider interface is tool-blind:

```go
type Provider interface { Name() string; Complete(ctx, req) (CompletionResponse, error) } // text in, text out
```

Tools exist only as prose in the system prompt, and tool calls are parsed back out of free text
(`extractJSON` grabs the outermost `{...}`). Every provider — including Anthropic and OpenAI, which have
reliable, structured, validated native tool-calling — is forced through this lowest-common-denominator
path. Tool params are `map[string]string` (name → human description): untyped, unusable for native APIs,
unvalidatable.

This is fragile in a way that bites security work. Driving the loop through `claude-cli`, the model
**fabricated** three non-existent projects (and a fake injection scenario) rather than call `list_projects`;
only a hardening prompt nudged it to actually call the tool. A prompt cannot *structurally* prevent
"pretend I called the tool" — native tool-use can, because the model can't answer with data it never
fetched. And we are about to add **gated, side-effecting** tools (send a request, create a finding, set
coverage) — a model that guesses a project list must not guess the arguments to `send_request`.

ADR-0006 already anticipated the fix ("native tool-use maps through directly; providers without it fall
back to structured tool-prompting"); we only ever built the fallback. This ADR makes tool use first-class.

## Decision

Introduce one **vendor-neutral tool language** the agent loop speaks, and a **per-provider translation
layer** that maps it to and from each backend's native format. Native tool-use where the provider has it;
a shared prompted fallback where it doesn't; identical downstream behavior either way.

### 1. Typed tool schema (source of truth)

Replace `Params map[string]string` with a typed schema — a pragmatic JSON-Schema subset:

```
ToolDef{ Name, Description, Params []Param }
Param{ Name, Type (string|integer|number|boolean|enum|array|object), Required bool, Description, Enum []string }
```

Every path (native render, prompted render, validation) derives from this one definition.

### 2. Two-layer provider abstraction

Keep the raw `Complete(messages) → text` (used by the prompted fallback and by non-tool calls — the
provider "test" round-trip, summaries). Add a **tool-aware** layer on top:

```
CompletionRequest{ Messages, Model, MaxTokens, Tools []ToolDef, ToolChoice }   // tools carried in
Completion{ Text string; ToolCalls []ToolCall; InputTokens, OutputTokens int }  // a call OR text
ToolCall{ ID, Name string; Args map[string]any }
```

The loop always passes `Tools` and receives either a final text answer or structured `ToolCall`s — it
never branches on provider.

### 3. Canonical tool-call / tool-result message model + persistence (the load-bearing change)

Native APIs require the transcript to carry the assistant's tool-call turn and a matching tool-result turn
**keyed by id** — not our current "feed the result back as a plain user message." So the neutral `Message`
gains tool turns:

```
Message{ Role (system|user|assistant|tool), Content string, ToolCalls []ToolCall, ToolCallID string }
```

Threads persist this neutral transcript (messages schema + `Advance`/`Resume` + UI rendering all updated).
Because the *neutral* form is the source of truth and each adapter re-renders it, **a thread started on
Anthropic can resume on Ollama** — vendor-portable history. This is the piece most easily underestimated;
it is what makes the translation correct rather than cosmetic.

### 4. Translation adapters + capability tiering

Each adapter maps outbound (neutral tools + transcript → native request) and inbound (native tool-call
structs → canonical `ToolCall`):

- **Anthropic** — `tools:[{name,description,input_schema}]`; `tool_use`/`tool_result` content blocks.
- **OpenAI-compatible** — `tools:[{type:"function",function:{…,parameters}}]`; `tool_calls` + `role:"tool"`
  messages with `tool_call_id`. (Azure/DeepSeek/Grok ride this.)
- **Ollama** — native tools where the server/model supports them; else the fallback.
- **claude-cli / plain completion** — no native tool API → a **shared prompted adapter** injects the
  tool-describing prompt (with the anti-fabrication hardening) over the raw `Complete` and parses the JSON
  into the same canonical `ToolCall`. Native vs prompted is an adapter detail; the loop is uniform.

A provider advertises tool support (native / prompted) so the tier is explicit and testable.

### 5. Argument validation before the gate

Validate each `ToolCall`'s args against the tool's schema (required present, types correct, enums valid)
*before* the approval gate and executor. Invalid → return a correction to the model, do not execute.

### 6. One tool call per turn (represent N)

Constrain the model to a single tool call per turn (Anthropic `tool_choice`, OpenAI
`parallel_tool_calls:false`, and the prompted contract) so every side-effecting call is individually
approved and audited — but model the canonical result as a list so we stay robust if a provider returns
several.

### 7. Governance unchanged

The approval gate, audit, and egress/DLP already operate on the canonical `ToolCall` and tool results;
they keep working as-is. Tool results returning to the model remain a DLP egress point.

## Consequences

- **Reliable *and* vendor-agnostic** — we no longer cripple the good providers, and the fabrication class
  of failure is structurally removed on native backends; the prompted fallback keeps claude-cli/local
  models working with the same downstream contract.
- **Vendor-portable transcripts** — the neutral tool-message model lets a conversation move across providers.
- **Ripple**: the messages schema, thread persistence, `Advance`/`Resume`, and the chat UI all change to
  carry tool turns. This is the real cost and must not be shortcut.
- **Build order (phased, each verifiable):**
  1. Typed `ToolDef` schema + arg validation (pure, unit-tested); migrate `analyst.Tools()` to it.
  2. Tool-aware provider interface + canonical tool messages + the **prompted adapter** (behavior identical
     to today, but structured) — prove parity end-to-end via claude-cli/mock.
  3. Persistence + `Advance`/`Resume` + UI for tool turns.
  4. Native adapters: OpenAI-compatible, then Anthropic; conformance suite (same scenario, every adapter,
     via fake vendor servers) + env-gated real-provider tests.
  5. Then expand the toolset (read exchanges, send_request, get/set_coverage, create_finding) on the solid base.
- **Out of scope now** (not precluded): streaming tool-use; full JSON-Schema (a subset suffices).

Supersedes the prompt-only tool protocol described in ADR-0006; coordinates with ADR-0011 (DLP at tool-result
egress) and ADR-0015 (the workbench surfaces the expanded toolset will reach into).
