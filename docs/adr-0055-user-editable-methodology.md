# ADR-0055 — User-editable methodology packs

Status: Accepted — building. Makes the methodology catalog (ADR-0009) authorable by operators instead of
code-only, applying the same "grow your own library" pattern that saved_playbooks (ADR-0019) gave agent
playbooks. Built-in and extension packs stay read-only; teams author their own packs — or copy a built-in and
edit it — and those packs become first-class citizens for adoption and coverage.

## Context

The methodology catalog shipped as three code-defined packs (`web-app`, `rest-api`, `oidc-oauth`) assembled by
`methodology.BuiltIns()`. The catalog surface said as much: *"There is no authoring today (the registry is
code-defined)."* But these packs are the translation of the team's real assessment checklists, and checklists
are living documents — a per-engagement or per-tech-stack check gets added, reworded, or dropped constantly.
Requiring a code change and release for every checklist edit is the wrong ownership model. Playbooks already
solved the same problem (ADR-0019 §4): built-ins in code, a `saved_playbooks` table for user records, and a
library UI where built-ins are read-only and "Copy" forks one into an editable record.

## Decision

Mirror the playbook stack for methodology:

- **Storage.** A `saved_methodologies` table in the global (instance-wide) DB — the catalog is instance-wide,
  like playbooks. `data` holds the full `methodology.Methodology` JSON; `id`/`title` are denormalized for
  listing. (Added to both `migrations/global/` for the two-tier layout and the legacy root set, matching
  `saved_playbooks`.)
- **Registry integration.** Saved packs are loaded into the in-memory `methodology.Registry` at startup
  (after built-ins and extensions) and kept in sync on create/update/delete. This is the key move: adoption,
  coverage, `Registry.Item` lookup, suggestions, and evidence counts already read the registry, so user packs
  behave *identically* to built-ins with no changes to those paths.
- **Immutability.** A pack is editable iff it has a `saved_methodologies` row. `UpdateSavedMethodology`
  returns `ErrNotFound` for any id without a row, so built-in and extension packs can't be edited or deleted
  even though they live in the same registry. The catalog list flags each pack `builtin` from the saved-id set
  so the editor shows built-ins/extensions read-only and offers "Copy" to fork them.
- **Validation.** `methodology.Normalize` derives a pack id and pack-scoped item ids from titles and applies
  `tech`/`version` defaults; `methodology.Validate` enforces a title, ≥1 titled item, and unique item ids. The
  API additionally rejects pack-id collisions (create) and cross-pack item-id collisions (item ids are global,
  since `Registry.Item` resolves by item id alone).
- **API / UI.** `POST /v1/methodologies`, `PUT|DELETE /v1/methodologies/{id}`. The read-only
  `MethodologyCatalog` becomes a library surface (New / Copy / Edit / Delete) hosting a `MethodologyBuilder`
  editor, structurally the twin of `PlaybookLibrary` + `PlaybookBuilder`.

## Consequences

- Item ids are globally unique across packs; the create/update path enforces it. Copying a built-in drops the
  source item ids so they're re-scoped under the new pack.
- Deleting a saved pack removes it from the registry but leaves any per-project adoption/coverage rows that
  referenced it; `BuildCoverage` skips packs the registry no longer knows. Retroactive cleanup of orphaned
  coverage rows is deferred (not observed to matter in practice — a deleted pack simply stops appearing).
- Extension-provided packs remain immutable through this path, which is correct: they're owned by the
  extension, not the operator. Editing one means copying it first.
