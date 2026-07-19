# ADR-0043 — Knowledge freshness

Status: Accepted — delivered. Knowledge-base entries carry a **last-verified** timestamp, and confirmed
facts go **stale** once they age past a kind-specific window — so accumulated knowledge doesn't silently
rot. The dossier flags stale entries; the agent (or a human) re-verifies a fact it re-observes rather than
letting it drift out of trust. The last of the four knowledge investments (capture → scope → dossier →
**freshness**).

## Context

The capture loop (ADR-0040), org inheritance (ADR-0041), and dossier (ADR-0042) let the workbench accumulate
durable knowledge that carries across engagements. But a fact captured a year ago read identically to one
confirmed this morning: a system's auth model, its endpoints, its deployment all change, and there was no
signal for *when* a fact was last known to be true. Left unaddressed, the KB becomes a pile of
confidently-stated but possibly-outdated claims — the failure mode of every long-lived knowledge base.

## Decision

**A `last_verified_at` on every KB entry (migration 0044).** When was this fact last affirmatively checked
to still hold. Set when a human-authored entry is created (confirmed = verified now) and when a draft is
**confirmed** (`ReviewKBEntry(confirmed)` stamps it — affirming a fact affirms it currently holds). An
unreviewed draft has no verification (NULL) — it has never been affirmatively checked; that's a distinct
state from "stale". The migration backfills existing confirmed entries to their last update.

**Kind-specific staleness windows (`pkg/dossier/freshness.go`).** How fast a fact changes drives how long it
stays fresh: structural facts (architecture, conventions, tactics) last a year; security posture and data
flows (auth, tech_stack, data_flow, gotcha) 180 days; concrete surface that moves with each release
(endpoints, environment) 90 days. `IsStale(kind, lastVerified, now)` — a confirmed fact older than its
window is stale; a never-verified draft is never "stale" (it's a draft). Pure and deterministic — no clock
baked in, `now` is passed in (so it's testable and the dossier stamps staleness at read time).

**Re-verify instead of re-draft (`verify_kb_entry`).** A `VerifyKBEntry(id)` store op bumps `last_verified_at`
to now **without changing review state** — so it's safe for the agent: a draft stays a draft (humans
confirm — the review gate is preserved), a confirmed fact just gets its freshness renewed. Surfaced as a
`verify_kb_entry` agent tool (added to every knowledge-writing profile, and called out in the scribe's
persona: when an assessment re-confirms a known fact, verify it rather than drafting a duplicate) and a
human `POST /v1/kb/{id}/verify` + `osb kb verify --id`.

**Surfacing staleness.** The dossier orders within a kind **fresh confirmed → stale confirmed → drafts**, and
tags stale entries `⚠️ _(stale — re-verify)_` next to the existing scope/draft tags; `list_kb` returns a
`stale` flag per entry so the agent sees at a glance what to re-check.

## Consequences

- **Knowledge ages honestly.** A fact's trust now decays with time unless re-verified, so the dossier
  distinguishes "known true recently" from "was true a year ago" — the reader (or agent) knows what to
  re-check before relying on it.
- **The capture loop closes.** capture (0040) → scope (0041) → consolidate (0042) → **keep current** (0043):
  the agent both adds new knowledge and refreshes what it re-observes, so a durable KB stays trustworthy
  across many engagements instead of drifting.
- **The human gate is preserved.** `verify_kb_entry` only renews freshness; it never confirms a draft, so an
  agent can't self-promote unverified knowledge — confirmation stays a human action (consistent with the
  observation/finding review discipline, ADR-0005).

## Out of scope — later
Auto-expiry or hiding of long-stale entries (today they're flagged, never dropped — a human decides);
a staleness summary/nudge surface in the workbench UI; re-verification playbook automation (a scheduled
"re-confirm the KB" run); per-entry override of the kind-default window; freshness-weighted RAG ranking
(down-rank stale chunks in `search_corpus`).

Composes with ADR-0010 (the KB), ADR-0040 (capture), ADR-0041 (scope), and ADR-0042 (the dossier this
freshness signal feeds).
