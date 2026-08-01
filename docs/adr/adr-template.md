# ADR-NNNN — Short decision title

Status: Proposed. One or two sentences stating the decision in the present tense — what we're doing
and, in a clause, why. This first paragraph is what the generated index shows, so make it a good
standalone summary. Update the leading word as the decision moves through its lifecycle: `Proposed`
→ `Accepted` → (later) `Superseded by ADR-MMMM`.

## Context

What's the situation that forces a decision? The constraints, the prior art (link related ADRs like
ADR-0001), the problem with doing nothing. State the forces at play, not the solution.

## Decision

What we're going to do, concretely. Be specific enough that someone can implement against it and
someone else can tell whether the code matches. Note what this supersedes or amends in earlier ADRs.

## Consequences

What becomes easier, what becomes harder, what we're accepting as a trade-off. Include follow-on
work this defers and anything a future reader should know before changing it.

<!--
Conventions (see docs/adr/README.md and ADR-0057):
- Copy this file to adr-NNNN-kebab-title.md using the next free number; never renumber or delete.
- Keep the `# ADR-NNNN — Title` heading and the `Status:` line — the index generator parses both.
- After adding or restatusing an ADR, run `make adr-index` and commit the regenerated index.
-->
