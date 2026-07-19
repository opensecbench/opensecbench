# ADR-0039 — RAG index over the project corpus

Status: Accepted — delivered. Semantic retrieval over a project's corpus + knowledge base: text is chunked,
embedded, and stored as vectors; a `search_corpus` tool (and API/CLI) returns the passages most relevant to
a query by meaning. It's the payoff for the tech-scout (ADR-0038) — the docs it gathers become retrievable.

## Context

The Analyst retrieved knowledge only lexically — `read_context` (whole doc, ≤96 KB), `grep_code`, and
`search` (SQL `LIKE`). As the corpus grows (tech-scout `save_context`, ingested docs, KB gotchas), whole-doc
reads don't scale and substring search misses the relevant passage. RAG adds meaning-based retrieval.

Two architecture facts fix the shape: **modernc SQLite is pure-Go (no CGO)**, so no native vector extension —
vectors are stored as **BLOB columns and ranked by brute-force cosine in Go** (fine to thousands of chunks;
ANN later). And there was **no embeddings path** — but the ollama/openai providers are already
OpenAI-compatible HTTP clients, so an `Embed` method reuses their transport.

## Decision

**Embeddings default local.** `llm.Embedder` (`Embed(ctx, []string) ([][]float32, error)`), implemented on
`*OpenAIProvider` (POST `/v1/embeddings`). A **dedicated** embedding config (`OSB_EMBED_BASE_URL` default
`http://127.0.0.1:11434/v1`, `OSB_EMBED_MODEL` default `nomic-embed-text`) separate from the completion
provider — so corpus text is embedded **on-host** even when completion is an external provider. A
deterministic `mockEmbedder` makes the whole pipeline testable without a server.

**Vector store (migration 0042).** `corpus_chunks(project_id, source_kind [context|kb], source_id,
source_name, chunk_index, text, embedding BLOB, dim, model)` — the schema's first BLOB (float32
little-endian). `ReplaceChunks` (delete-then-insert per source → idempotent re-index), `SearchChunks`
(brute-force cosine, top-k, dimension-guarded so a stale model can't match), `DeleteChunksForSource`,
`CountChunks`.

**Chunking + indexing (`pkg/rag`).** A paragraph-aware chunker (~1000 chars, ~150 overlap). `Indexer`
(`IndexContextItem` via the `read_context` load chain; `IndexKBEntry` inline; `Reindex(project)` rebuilds
all; `Search` embeds the query then `SearchChunks`). Indexing is **best-effort** — a missing/unreachable
embedder never fails the underlying write.

**Wiring.** Index-on-write after `save_context`/`ingestContext`/`draft_kb_entry`. A `search_corpus` agent
tool (added to the `reads` bundle) is the semantic counterpart to `search`, and is **egress-gated** exactly
like `read_context` (returning corpus/KB text to an external model under strict policy is blocked). Backfill
+ direct use via `POST /v1/projects/{id}/reindex`, `GET .../search-corpus?q=&k=`, and `osb rag
reindex|search`.

## Consequences

- **Meaning-based retrieval.** The agent finds the right passage ("what do we know about X", tool gotchas)
  across a large corpus, not just substring hits — and the tech-scout's gathered docs are now usable.
- **Corpus stays on-host.** Embeddings default to a local endpoint (ollama is `IsLocal`), so corpus text is
  not sent to an external service to be embedded; retrieval to the model is egress-gated like `read_context`.
- **No new dependency.** BLOB + Go cosine under modernc SQLite; no CGO, no vector extension.
- **Local embedder required for real quality.** v1 needs ollama (or any OpenAI-compat embeddings endpoint) —
  when it's down, indexing is skipped best-effort and `search_corpus`/reindex return a clear error naming the
  endpoint. The pipeline is proven with the deterministic mock embedder.

## Out of scope — later
ANN index (HNSW/IVF) for very large corpora; re-ranking; hybrid lexical+semantic fusion; indexing findings/
observations (v1 = corpus + KB); a pure-Go local embedder (removes the ollama dependency); change-detection
beyond replace-on-write; a friendlier "run ollama" message when the configured endpoint is unreachable.

Composes with ADR-0038 (tech-scout fills the corpus), ADR-0020 (corpus/`read_context`), ADR-0010 (KB),
ADR-0011 (egress guard, mirrored), and ADR-0006/0021 (the provider/config layer the embedder reuses).
