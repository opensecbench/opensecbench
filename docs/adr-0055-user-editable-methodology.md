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

- Item ids are globally unique across packs; the create/update path enforces it via the shared
  `methodology.CheckItemCollisions` (used by both the HTTP handlers and the agent tool). Copying a built-in
  drops the source item ids so they're re-scoped under the new pack.
- Extension-provided packs remain immutable through this path, which is correct: they're owned by the
  extension, not the operator. Editing one means copying it first.

## Follow-ups (delivered)

Closing the loop after the initial build:

- **Agent-authoring parity (ADR-0053).** A `save_methodology` agent tool authors packs through the exact
  `Normalize → Validate → CheckItemCollisions → persist → Register` path the HTTP handler uses, so an
  agent-authored pack is indistinguishable from a human's and immediately adoptable/coverable. It's reversible
  (a human can edit/delete it), so it stays out of `toolConsequence` and runs ungated like `create_finding`.
  The registry is threaded into the analyst via `ExecDeps.Methods` / `Service.SetMethods`; nil in headless
  runs makes the tool a no-op. Built-in/extension packs stay immutable (edit → error).
- **Validated capability picker.** The editor's per-item "suggested capabilities" field is now a picker backed
  by `GET /v1/capabilities`, so items only ever reference capabilities that exist (no free-text drift).
- **Orphan cleanup on delete.** Deleting a pack now sweeps per-project adoption + coverage:
  `Manager.PurgeMethodologyPack` iterates every project, unadopting the pack and deleting
  `methodology_coverage`/`coverage_observations` rows for its item ids. Best-effort — a sweep failure is
  audited but doesn't fail the delete, since orphaned rows are harmless (`BuildCoverage` skips unknown packs).

## On-ramps: checklist conversion + import/export

The structured item model (objective / procedure / standards / capabilities) is what makes coverage tracking
work, but hand-authoring it is friction. Two on-ramps let teams keep their existing loose checklists and still
land in the structured model:

- **Paste-a-checklist (LLM).** `POST /v1/methodologies/draft` → `Service.ConvertChecklist` runs a single
  cheap-tagged LLM completion that maps free-form checklist text into the pack schema (reusing the narrator's
  tolerant `extractJSONObject`). It does **not** persist — it returns an unsaved draft that opens in the editor
  for review, so LLM output always gets a human glance before entering the catalog. The prompt is constrained
  to real capability ids (from the engine's registry) so suggestions validate. This is the human-facing twin
  of the `save_methodology` agent tool: same structured target, different entry point.
- **Import / export JSON.** Deterministic round-trip, no model involved. Export is client-side (serialize the
  pack, strip the transient `builtin` flag, download `methodology-<id>.json`); import parses a `.json` file and
  opens it as a draft through the same review-then-save path. Reuses the existing create endpoint — no new
  backend. Good for sharing packs between instances or restoring a deleted one.

Both funnel through one path in the editor: draft → review → save. Copy, import, and convert all just pre-fill
the builder in create mode; only an explicit Edit saves in place.
