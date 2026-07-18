# ADR-0006 — Agent runtime & providers

Status: Accepted

## Context

The Analyst (the AI persona) must reason across authorized project context and, gated, run the
same capabilities a human uses (ADR-0001, ADR-0003). We build our own agent runtime rather than
delegating to a vendor agent CLI, so that tool-calling, approval, audit, budgets, and data-egress
policy stay in our hands and work across many LLM backends (Anthropic API, AWS Bedrock,
Azure/OpenAI, Vertex/Gemini, local models, and an optional `claude` CLI).

## Decision

**Providers are inference-only.** A provider turns a list of messages into a text completion:

```
Provider{ Name() string; Complete(ctx, CompletionRequest) (CompletionResponse, error) }
CompletionRequest{ Messages []Message, Model string, MaxTokens int }
CompletionResponse{ Text string, InputTokens int, OutputTokens int }
```

Adapters implement this over different backends:

- **Mock** — scripted responses for tests.
- **CLI-binary** — invokes a configurable binary (default `claude -p`) for a one-shot completion.
  We do *not* hand the agentic loop to the binary; we use it as an inference source.
- **HTTP** — a provider's REST API (Anthropic Messages first; others follow the same shape).

**We own the tool-calling loop** (`pkg/agent`). Because not every backend exposes native tool-use,
the loop uses **structured tool-prompting** as the uniform mechanism: a system prompt describes the
available tools and instructs the model to reply with a single JSON object — either a tool call
`{"tool": "...", "args": {...}}` or a final `{"answer": "..."}`. The loop parses the reply, and:

1. On a **tool call**, it passes through the **approval gate**, executes the tool via a pluggable
   executor (capabilities run in sandboxed runners; read-only queries run directly), appends the
   result as a message, and continues.
2. On an **answer**, it stops and returns the transcript + final text.

Every step is **audited**; an **iteration cap** bounds runaway loops (budgets refine this later).
Native tool-use can later be an optimization for backends that support it, without changing the
loop's contract.

## Consequences

- The Analyst never gets a raw host shell; it acts only through the tool executor, which is the
  approval + audit boundary (ADR-0001).
- Adding a backend is writing a `Provider` adapter; swapping providers is configuration.
- Structured tool-prompting is model-dependent in reliability; the loop validates/repairs JSON and
  caps iterations. This is acceptable for a first cut and improves with native tool-use later.
- Data-egress policy (which provider may see which asset by sensitivity) and agent budgets attach
  at the loop/provider boundary; they are specified here and implemented as the policy engine lands.
- Threads persist the message history and support forking (a cached prefix explored in parallel).
