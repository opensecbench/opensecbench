# ADR-0041 — Org/team-level knowledge scope

Status: Accepted — delivered. Knowledge-base entries can now be anchored above a single target — to an
**organization** (or group, or globally) — and a project **inherits** all levels. So a fact learned once
about how a team is set up (a shared auth provider, org-wide conventions, common infra) applies across every
one of that org's apps and future engagements, instead of being trapped on one target.

## Context

The KB was durable and carried across engagements, but every entry was anchored to exactly one `target`
(`kb_entries.target_id NOT NULL`). The `scope` column and the `group`/`org`/`global` constants existed but
were **unused** — no columns to anchor them, no inheritance walk. So org/team-level knowledge had nowhere to
live and couldn't compound across a team's applications. This is the second knowledge investment (after
ADR-0040's capture loop); a dossier view and freshness are the remaining ones.

## Decision

**Anchor above target (migration 0043).** `kb_entries` gains nullable `group_id` and `organization_id`, and
`target_id` becomes nullable, with a CHECK that exactly the right anchor is set for the `scope`
(target→target_id, group→group_id, org→organization_id, global→none). SQLite can't drop a NOT NULL in place,
so the table is recreated and existing rows are copied as `target`-scoped (verified live: clean apply to
schema 43).

**Inheritance on read.** `ListKBByProject` now returns the union of the project's **target(s) + its group +
its organization (and the orgs of its linked targets) + global** entries, ordered **most-specific first**
(target → group → org → global). `ListKBByTarget` returns the target + its org + global. So re-assessing a
known org starts with everything the team learned before — not just the one system — while a different org's
knowledge is never inherited.

**Writing scoped knowledge.** `draft_kb_entry` gains a `scope` param (`target` default | `org` | `global`);
for `org` it resolves the organization from the project (or the given target) so the agent needn't fetch org
ids. The `knowledge-scribe` (ADR-0040) is instructed to capture organization-wide facts at `org` scope and
system-specific facts at `target` scope.

## Consequences

- **Team knowledge compounds.** "This org uses Keycloak / deploys on Cloud Run / requires a bearer token on
  every /api route" is captured once at org scope and every app under that org inherits it — the core of
  building institutional memory across a team.
- **Specificity ordering.** Retrieval surfaces the most specific knowledge first (a target override before
  the org default), so per-system nuances aren't drowned out.
- **No isolation break.** Inheritance is scoped to the project's own org/group/targets; one org never sees
  another's knowledge; global is opt-in (nothing writes it by default).
- **Human-confirmed still.** Scoped drafts follow the same review discipline — unreviewed until confirmed.

## Out of scope — later
Human API/UI to create org/group/global entries (the create route is target-scoped today; the scribe writes
org scope); a **group** management API (groups exist in the schema but have no CRUD, so group-scope is
reachable only if a group id is already set); the **dossier** synthesis view and **freshness** (the next two
knowledge investments); making RAG index inherited entries once per org rather than per project.

Composes with ADR-0010 (the KB), ADR-0040 (the scribe that now writes scoped knowledge), and ADR-0002 (the
org/group/target entity model this finally uses).
