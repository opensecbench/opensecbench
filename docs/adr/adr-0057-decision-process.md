# ADR-0057 — ADR process & contribution decisions

Status: Accepted. Documents how design decisions are made and recorded now that the project is
public: when a change needs an ADR, the status lifecycle a decision moves through, who accepts one,
and the **propose-before-building-big** rule for outside contributions. Makes the working agreement
that has been implicit since ADR-0001 explicit for contributors.

## Context

OpenSecBench has been documented decision-first from the start: a short ADR lands before a subsystem
is built, and ADRs are kept current as decisions change (ADR-0001). That worked well with a single
author making every call. Going public changes the shape of the problem — contributors who didn't
write the existing 56 ADRs now need to know when *they* must write one, what a well-formed ADR looks
like, and how a proposed decision becomes an accepted one. Left unwritten, either every trivial PR
gets bogged down in process, or large changes land against decisions a maintainer would have
rejected. Both are avoidable.

## Decision

**When an ADR is required.** Tier by the kind of change, not its size in lines:

- **No ADR:** bug fixes, small features, docs, tests, refactors — anything that fits the existing
  design. Align the change with the relevant ADR and reference it in the PR.
- **ADR required, in the same PR:** a new subsystem or capability class; a change to a documented
  decision; a new or changed on-the-wire/on-disk format, protocol, or extension-package shape; or
  anything cross-cutting that a future reader would ask "why was this done this way?" about.

When in doubt, open an issue and ask — it's cheaper than guessing.

**Propose before building big.** For any change that needs an ADR, open a `Proposed` ADR (or an
issue that will become one) *before* writing the large PR. The design gets reviewed before the code,
so nobody invests days against a decision that won't be accepted. Small changes skip this entirely.

**Status lifecycle.** An ADR's `Status:` line starts with one of:

- `Proposed` — under discussion; not yet agreed. Safe to open a draft PR against, but don't rely on it.
- `Accepted` — the decision stands; the code should match it (append qualifiers like
  "Accepted — Phase 1 delivered" as work progresses).
- `Superseded by ADR-MMMM` — a later ADR replaced this decision. The old ADR stays in the tree for
  provenance; it is never deleted or silently rewritten.

**Who accepts.** OpenSecBench currently has a maintainer-led model: a contributor authors a
`Proposed` ADR, and a maintainer moves it to `Accepted` (typically on merge) or requests changes.
This will grow into something more formal if the maintainer set grows; until then, acceptance is a
maintainer decision recorded by the status change landing on `main`.

**Numbering, format, and the index.**

- Number ADRs sequentially with the next free four-digit number; never renumber or delete one.
- Format is **Context → Decision → Consequences**; copy [`adr-template.md`](adr-template.md) to start.
- Keep the `# ADR-NNNN — Title` heading and the `Status:` line intact — they are the source of
  truth for the index.
- The index table in [`README.md`](README.md) is **generated** from those two fields by
  `make adr-index` (`scripts/gen_adr_index.go`), so it can't drift. After adding or restatusing an
  ADR, run it and commit the result; CI regenerates and fails the build if the committed index is
  stale. Do not hand-edit the table.

The short version of all this lives in [`CONTRIBUTING.md`](../../CONTRIBUTING.md); this ADR is the
authoritative long form.

## Consequences

- Contributors have a clear, low-friction rule: most PRs need no ADR, and the ones that do are
  flagged early rather than after the work is done.
- The index stays correct for free — the rot that motivated this (a hand-maintained table that fell
  out of sync) can't recur, because the ADRs are the single source of truth.
- Process is deliberately lightweight and maintainer-led. If governance needs to scale (multiple
  maintainers, a formal RFC period, voting), that's itself a decision — it gets a new ADR that
  supersedes the "who accepts" section here.
- The generator adds a tiny build-time dependency (a `go run` step in CI and a Make target); it uses
  only the standard library and is excluded from `go build ./...` by a `//go:build ignore` tag.
