# ADR-0049 — Per-project storage cohesion (project = a self-contained directory)

Status: Accepted — planned. Everything a project owns — its structured rows, its evidence blobs, and its
agent workspace — moves under a single per-project directory, so an engagement can be **purged, backed up,
or migrated** as one filesystem object. Cross-cutting config stays in a small `global.db`; durable
cross-engagement knowledge gets its own `targets/` tier. Adopted by **wipe-and-adopt** while data is still
disposable — no data-split migration is written.

## Context

Today the whole instance is one global SQLite database plus a set of sibling stores under
`~/.config/opensecbench/`, all resolved from `filepath.Dir(DBPath)`:

- `opensecbench.db` — every table, project-scoped rows tagged by `project_id`. Some large payloads are
  stored **inline**: `http_exchanges` request/response bodies (2 MiB cap/body, no dedup), `corpus_chunks`
  text + embedding BLOBs.
- `cas/` — a single global content-addressed store (SHA-256, idempotent). Holds report bytes, task/capability
  output, ingested context, session transcripts. **Blobs carry no `project_id`**; scoping lives only in the
  `artifacts`/`reports`/`context_items` rows that reference the hash.
- `workspace/<projectId>/` — a mutable per-project scratch filesystem for the agent (drafts, PoCs,
  `run_code` output). Not content-addressed, not tracked in the DB.

Two structural gaps follow from the shared stores. `DeleteProject` is just `DELETE FROM projects` (cascade),
so deleting a project **orphans its CAS blobs** (nothing refcounts or sweeps them) and **orphans
`workspace/<projectId>/`** (never tracked, never removed). And the DB never shrinks after a delete without
`VACUUM`. In light testing the DB reached ~51 MB (almost entirely inline `http_exchanges` proxy bodies)
while CAS stayed at 8 KB — a warning shot for real engagements, some of which are large.

`bundle.Export`/`Import` already exists but solves a *different* problem: a curated, encrypted, ID-remapped
**logical subset** (project → findings → evidence → KB) for handing an engagement to someone else. It
deliberately omits the bulky operational data (threads, proxy traffic, corpus, workspace). It is the "share"
primitive, not the "purge/backup/migrate the whole thing cheaply" primitive.

The goal this ADR optimizes for — explicitly chosen over cross-project query convenience — is **operational
cohesion**: purge/backup/migrate an engagement as one unit. (This reverses the interim recommendation to keep
a single DB, which optimized for a different target.)

## Decision

**A project is a self-contained directory. Nothing project-scoped lives in a shared store.**

```
~/.config/opensecbench/
├── global.db            cross-cutting ONLY: org/group tree, settings, providers, runners,
│                        extensions registry, audit chain, + a thin project INDEX
│                        (id, name, status, summary counts) for cross-project listing
├── vault.key            instance key material (global)
├── proxy-ca/            shared CA (global)
├── extensions/          installed packages (global)
├── targets/             DURABLE, cross-engagement knowledge
│   └── <targetId>/
│       ├── target.db    KB, coverage history, corpus_chunks for the durable target
│       └── cas/         evidence owned by the durable target
└── projects/
    └── <projectId>/
        ├── project.db   ALL project-scoped rows: tasks, artifacts index, observations,
        │                findings, threads, http_exchanges(meta), plans, dispositions, routes…
        ├── cas/         blobs this project owns (report PDFs, task output, exchange bodies)
        ├── workspace/   agent scratch (drafts, PoCs, run_code)
        └── meta.json    id/name/created/schema-version — self-describing for import
```

**Three storage domains, explicitly:**

1. **Global** (`global.db` + `vault.key` + `proxy-ca/` + `extensions/`) — instance-level config and identity,
   never purged with a project: org/group hierarchy, settings, providers, runners, extensions, the
   hash-chained **audit trail** (audit events survive project deletion by design), and a denormalized
   **project index** (id/name/status/counts) so cross-project dashboards read one place instead of fanning
   out across N databases.

2. **Project** (`projects/<id>/`) — one engagement, fully self-contained: its own `project.db`, its own `cas/`,
   its own `workspace/`. This is the unit of purge/backup/migrate.

3. **Durable target** (`targets/<id>/`) — knowledge that must outlive any single engagement (KB, coverage
   history, corpus). Kept out of ephemeral projects so purging an engagement can never destroy shared
   knowledge. A project links to a target; KB reads span the project's own corpus plus the linked target's.

**Consequent operations** become filesystem-trivial:

| Goal | Operation |
|------|-----------|
| Purge | `rm -rf projects/<id>/` + drop the index row — reclaims DB **and** CAS **and** workspace at once |
| Backup | `tar czf <id>.tgz projects/<id>/` — self-contained |
| Migrate | copy the dir to another instance, re-register in the index (UUID ids, no collisions) |

**Adoption: wipe-and-adopt.** Current data is disposable test data. The new layout ships and the existing
`~/.config/opensecbench` is deleted; **no data-split migration is written.** This must land before real
engagements begin.

## Consequences

- **The CAS-GC and workspace-cleanup gaps disappear.** Nothing project-owned is global, so there are no
  orphans to sweep — purge is `rm`. No refcounting/GC subsystem is needed.
- **The DB size problem is contained.** `http_exchanges` bodies move into the project's own `cas/`
  (deduped, per-project); proxy bloat is bounded per engagement and vanishes on purge. (Body-in-CAS +
  retention is folded into Phase 2.)
- **CAS stops being a singleton** — the biggest code change. Today one `*cas.Store` is injected everywhere
  (~10 `Put` sites); it becomes a `StoreFor(projectID)`/`StoreForTarget(targetID)` resolver with `projectID`
  threaded to those call sites.
- **The store layer becomes a router** — a `global.db` handle plus an on-demand per-project handle pool
  (open lazily, LRU-close); migrations run per-project-db on first open. Global-scoped methods hit
  `global.db`; project-scoped methods hit the project handle.
- **Cross-project CAS dedup is lost** — accepted, even preferred: a globally-shared blob is precisely what
  makes clean purge impossible. Per-project isolation is worth some duplicate bytes in a security tool.
- **Cross-project queries** ("all open findings everywhere") are served by the `global.db` index, updated on
  write — not by fanning out across project databases.
- **`bundle` is unaffected** and remains the curated-share path. An `export --raw` (tar the dir) may be added
  later as the cheap full-fidelity archive.

### Rollout phases

0. **Layout + wipe** — adopt the directory structure; delete existing data (no splitter). This ADR.
1. **DB router** — `global.db` + per-project handle pool; per-project migration-on-open; move project-scoped
   tables out of the global schema.
2. **Per-project CAS** — `StoreFor(projectID)` resolver; thread `projectID` to `Put`/`Open` sites; route
   `http_exchanges` bodies into the project CAS; add proxy-exchange retention.
3. **Lifecycle commands** — `osb project purge|export --raw|import-dir`; workspace cleanup falls out for free
   (it's inside the project dir).
4. **Durable `targets/` tier** — `target.db` + `cas/` per target; KB/corpus read path spans project + linked
   targets.

## Out of scope — later

- A Postgres/pluggable store backend for a hosted multi-tenant deployment — the store-router seam introduced
  here is the right place to add it, but this ADR stays SQLite-on-disk.
- A data-split migration from the legacy single-DB layout (intentionally skipped — wipe-and-adopt).
- Cross-instance sync / replication of `global.db`.
- Encrypting per-project directories at rest (today only `bundle` exports are sealed).
